package journal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/doctor/check"
)

// IDs of the journal diagnostics.
const (
	DoctorIDLocation = "journal.location"
	DoctorIDEntries  = "journal.entries"
	DoctorIDStale    = "journal.stale"
	DoctorIDGC       = "journal.gc"
	DoctorIDStray    = "journal.stray"
	DoctorIDIndex    = "journal.index"
)

// maxEvidence bounds the paths listed in one diagnostic.
const maxEvidence = 10

// DoctorChecks supplies journal diagnostics for registration with doctor.Service.
func DoctorChecks(root string) []check.Check {
	s := newStore(root)
	return []check.Check{
		{
			ID:  DoctorIDLocation,
			Run: s.checkLocation,
		},
		{
			ID:  DoctorIDEntries,
			Run: s.checkEntries,
		},
		{
			ID:  DoctorIDStale,
			Run: s.checkStale,
		},
		{
			ID:  DoctorIDGC,
			Run: s.checkGC,
		},
		{
			ID:  DoctorIDStray,
			Run: s.checkStray,
		},
		{
			ID:  DoctorIDIndex,
			Run: s.checkIndex,
		},
	}
}

func (s store) checkLocation(ctx context.Context) check.Result {
	if inside, err := git(ctx, s.root, "rev-parse", "--is-inside-work-tree"); err != nil || inside != "true" {
		return check.Result{
			Status:  check.StatusSkipped,
			Message: "Not a Git work tree, or Git is unavailable; ignore rules were not checked",
		}
	}

	buildDir, err := config.ProjectBuildDir(s.root)
	if err != nil {
		return check.Result{
			Status:  check.StatusWarn,
			Message: "Project configuration cannot be loaded, so ignore rules were not checked",
		}
	}

	paths := []string{config.JournalDir}
	rel, err := filepath.Rel(s.root, buildDir)
	if err == nil && filepath.IsLocal(rel) {
		paths = append(paths, filepath.ToSlash(rel))
	}

	var exposed []string
	for _, path := range paths {
		// A path inside the directory matches directory patterns whether or not it exists.
		// #nosec G204 -- Git and its options are fixed; the path follows -- and no shell is used.
		cmd := exec.CommandContext(ctx, "git", "-C", s.root, "check-ignore", "-q", "--no-index", "--", path+"/probe")
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			exposed = append(exposed, path+"/")
		default:
			return check.Result{
				Status:  check.StatusWarn,
				Message: "Git ignore rules could not be checked",
			}
		}
	}
	if len(exposed) > 0 {
		return check.Result{
			Status:      check.StatusFail,
			Message:     "Journal or build directory is not ignored by Git, so its files could be committed or indexed",
			Remediation: "go42x agentenv update, or add these directories to .gitignore",
			Evidence:    exposed,
		}
	}

	return check.Result{
		Status:   check.StatusPass,
		Message:  "Journal and build directories are ignored by Git and the knowledge base",
		Evidence: paths,
	}
}

func (s store) checkEntries(ctx context.Context) check.Result {
	current, err := s.scan(ctx)
	if err != nil {
		return check.Result{
			Status:  check.StatusFail,
			Message: "Journal cannot be read",
		}
	}

	var evidence []string
	for _, problem := range current.invalid {
		evidence = append(evidence, problem.Path+": "+problem.Error)
	}
	for _, entry := range current.entries {
		for _, path := range sensitiveFiles(s.entryDir(entry.ID)) {
			evidence = append(evidence, s.relative(path)+": file name suggests credentials")
		}
	}
	if len(evidence) > 0 {
		return check.Result{
			Status:      check.StatusWarn,
			Message:     "Journal entries have invalid records or files that may hold credentials",
			Remediation: "Fix or remove the listed records and files",
			Evidence:    limitEvidence(evidence),
		}
	}

	return check.Result{
		Status:  check.StatusPass,
		Message: fmt.Sprintf("%d journal entries are valid", len(current.entries)),
	}
}

func (s store) checkStale(ctx context.Context) check.Result {
	now := time.Now()
	current, err := s.scan(ctx)
	if err != nil {
		return check.Result{
			Status:  check.StatusFail,
			Message: "Journal cannot be read",
		}
	}

	var evidence []string
	for _, entry := range current.entries {
		if entry.Status == StatusOpen && now.Sub(entry.UpdatedAt) > StaleAfter {
			evidence = append(evidence, fmt.Sprintf("%s: updated %s", entry.ID, entry.UpdatedAt.Format(time.DateOnly)))
		}
	}
	if len(evidence) > 0 {
		return check.Result{
			Status: check.StatusWarn,
			Message: fmt.Sprintf("%d open journal entries were not updated for more than %d days",
				len(evidence), int(StaleAfter.Hours()/24)),
			Remediation: "Continue the entries, or finish them with go42x agentenv journal done <id> --abandoned",
			Evidence:    limitEvidence(evidence),
		}
	}

	return check.Result{
		Status:  check.StatusPass,
		Message: "No stale open journal entries",
	}
}

func (s store) checkGC(ctx context.Context) check.Result {
	now := time.Now()
	current, err := s.scan(ctx)
	if err != nil {
		return check.Result{
			Status:  check.StatusFail,
			Message: "Journal cannot be read",
		}
	}

	var size int64
	var evidence []string
	for _, entry := range current.entries {
		if expired(entry, now) {
			size += treeSize(s.entryDir(entry.ID))
			evidence = append(evidence, entry.ID)
		}
	}
	if len(evidence) > 0 {
		return check.Result{
			Status:      check.StatusWarn,
			Message:     fmt.Sprintf("%d expired journal entries can be removed (%s)", len(evidence), FormatSize(size)),
			Remediation: "go42x agentenv journal gc --apply",
			Evidence:    limitEvidence(evidence),
		}
	}

	return check.Result{
		Status:  check.StatusPass,
		Message: "No expired journal entries",
	}
}

func (s store) checkStray(ctx context.Context) check.Result {
	current, err := s.scan(ctx)
	if err != nil {
		return check.Result{
			Status:  check.StatusFail,
			Message: "Journal cannot be read",
		}
	}
	build, err := s.strayBuild(ctx)
	if err != nil {
		return check.Result{
			Status:  check.StatusFail,
			Message: "Build directory cannot be read",
		}
	}

	items := append(current.stray, build...)
	if len(items) == 0 {
		return check.Result{
			Status:  check.StatusPass,
			Message: "Journal and build directories hold no content without a record",
		}
	}

	var size int64
	evidence := make([]string, 0, len(items))
	for _, item := range items {
		size += item.Size
		evidence = append(evidence, item.Path)
	}
	return check.Result{
		Status: check.StatusWarn,
		Message: fmt.Sprintf("%d items in the journal or build directory have no journal record (%s)",
			len(items), FormatSize(size)),
		Remediation: "Review them with go42x agentenv journal list --stray and delete what is no longer needed; " +
			"agents ask the user first",
		Evidence: limitEvidence(evidence),
	}
}

func (s store) checkIndex(ctx context.Context) check.Result {
	current, err := s.scan(ctx)
	if err != nil {
		return check.Result{
			Status:  check.StatusFail,
			Message: "Journal cannot be read",
		}
	}

	stored, err := os.ReadFile(filepath.Join(s.dir, indexFile))
	if errors.Is(err, fs.ErrNotExist) && len(current.entries) == 0 {
		return check.Result{
			Status:  check.StatusPass,
			Message: "No journal entries to index",
		}
	}

	want, renderErr := renderIndex(current.entries)
	if err != nil || renderErr != nil || string(stored) != string(want) {
		return check.Result{
			Status:      check.StatusWarn,
			Message:     "Journal index is missing or does not match the entries",
			Remediation: "go42x agentenv journal index",
		}
	}

	return check.Result{
		Status:  check.StatusPass,
		Message: fmt.Sprintf("Journal index lists all %d entries", len(current.entries)),
	}
}

// sensitiveFiles lists files below dir whose names suggest credentials.
func sensitiveFiles(dir string) []string {
	var found []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}

		name := strings.ToLower(entry.Name())
		if name == ".env" || strings.HasPrefix(name, ".env.") ||
			strings.HasPrefix(name, "id_rsa") || strings.HasPrefix(name, "id_dsa") ||
			strings.HasPrefix(name, "id_ecdsa") || strings.HasPrefix(name, "id_ed25519") {
			found = append(found, path)
			return nil
		}
		switch filepath.Ext(name) {
		case ".pem", ".key", ".p12", ".pfx":
			found = append(found, path)
		}
		return nil
	})
	return found
}

func limitEvidence(evidence []string) []string {
	if len(evidence) <= maxEvidence {
		return evidence
	}
	return append(evidence[:maxEvidence:maxEvidence], fmt.Sprintf("and %d more", len(evidence)-maxEvidence))
}

// FormatSize renders a byte count with a binary unit.
func FormatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	value, exponent := float64(size)/unit, 0
	for value >= unit && exponent < 4 {
		value /= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", value, "KMGTP"[exponent])
}
