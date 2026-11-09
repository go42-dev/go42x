package agentenv

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

// bundleSourceFile records the go42x revision whose bundle the project's sources came
// from. An update fetches that bundle as the base of its three-way merge.
const bundleSourceFile = ".bundle.lock.yaml"

// bundleSourceDir is the bundle's directory in the go42x repository.
const bundleSourceDir = "assets/agentenv"

const bundleFetchTimeout = 2 * time.Minute

// abbreviatedCommit matches the commit in a Go pseudo-version, which Git cannot fetch by name.
var abbreviatedCommit = regexp.MustCompile(`^[0-9a-f]{7,39}$`)

func parseBundleSource(data []byte) (*buildinfo.Source, error) {
	var source buildinfo.Source
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&source); err != nil {
		return nil, err
	}
	if err := validateBundleSource(&source); err != nil {
		return nil, err
	}

	return &source, nil
}

// validateBundleSource limits the record to repository URLs and refs, so it cannot pass
// Git options or select Git helper transports.
func validateBundleSource(source *buildinfo.Source) error {
	u, err := url.Parse(source.Repository)
	if err != nil || !slices.Contains([]string{"file", "http", "https", "ssh"}, u.Scheme) {
		return fmt.Errorf("repository must be a file, http, https, or ssh URL: %q", source.Repository)
	}
	if len(source.Ref) == 0 || strings.HasPrefix(source.Ref, "-") ||
		strings.ContainsFunc(source.Ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("ref must be a commit or tag: %q", source.Ref)
	}

	return nil
}

// marshalBundleSource encodes the record as a YAML document that starts with an explicit
// document marker, like the bundled YAML sources.
func marshalBundleSource(source *buildinfo.Source) ([]byte, error) {
	data, err := yaml.Marshal(source)
	if err != nil {
		return nil, err
	}

	return append([]byte("---\n"), data...), nil
}

// prepareBundleSource schedules the record of source in dir. A nil source removes the
// record, because the installed sources then come from a build that no update can fetch.
func prepareBundleSource(plan *fspatch.Plan, dir string, source *buildinfo.Source) error {
	target := filepath.Join(dir, bundleSourceFile)
	if source == nil {
		_, exists, err := plan.Read(target)
		if err != nil || !exists {
			return err
		}

		return plan.Remove(target, fspatch.KindSources)
	}

	data, err := marshalBundleSource(source)
	if err != nil {
		return err
	}

	return plan.Write(target, data, fspatch.KindSources, false)
}

// writeBundleSource records source in dir like prepareBundleSource, but writes directly
// for an initialization, which keeps no backups.
func writeBundleSource(dir string, source *buildinfo.Source) error {
	target := filepath.Join(dir, bundleSourceFile)
	if source == nil {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}

		return nil
	}

	data, err := marshalBundleSource(source)
	if err != nil {
		return err
	}

	// #nosec G306 -- The record is a public project file, like the bundled sources.
	return os.WriteFile(target, data, 0644)
}

// readBundleSource reads the project's record of its previous bundle from dir.
func readBundleSource(plan *fspatch.Plan, dir string) (*buildinfo.Source, error) {
	name := filepath.ToSlash(filepath.Join(config.Directory, bundleSourceFile))
	data, exists, err := plan.Read(filepath.Join(dir, bundleSourceFile))
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%s is missing, so the previous bundle is unknown; run go42x agentenv init --force "+
			"to reinstall the bundle, which replaces local changes to bundled files", name)
	}

	source, err := parseBundleSource(data)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	return source, nil
}

// fetchBundle reads the bundle at source with a shallow, sparse Git fetch into a
// repository under workspace. An abbreviated commit, which Git cannot fetch by name, is
// resolved from the repository's branches and tags without downloading their files.
func fetchBundle(ctx context.Context, source *buildinfo.Source, workspace string) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, bundleFetchTimeout)
	defer cancel()

	repository := filepath.Join(workspace, "source")
	if err := os.MkdirAll(repository, 0700); err != nil {
		return nil, err
	}

	git := func(args ...string) (string, error) {
		// #nosec G204 -- Git is fixed, the record is validated, and no shell is used.
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repository}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		cmd.WaitDelay = time.Second
		var output, diagnostics bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &diagnostics
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}

			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(diagnostics.String()))
		}

		return strings.TrimSpace(output.String()), nil
	}

	for _, args := range [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", source.Repository},
		{"sparse-checkout", "set", "--no-cone", "/" + bundleSourceDir + "/"},
	} {
		if _, err := git(args...); err != nil {
			return nil, err
		}
	}

	commit := "FETCH_HEAD"
	if abbreviatedCommit.MatchString(source.Ref) {
		if _, err := git("fetch", "--quiet", "--filter=blob:none", "--no-tags", "origin",
			"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"); err != nil {
			return nil, err
		}

		resolved, err := git("rev-parse", "--verify", "--quiet", source.Ref+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("commit %s is not on a branch or tag: %w", source.Ref, err)
		}
		commit = resolved
	} else if _, err := git("fetch", "--quiet", "--depth=1", "--filter=blob:none", "--no-tags",
		"origin", source.Ref); err != nil {
		return nil, err
	}

	if _, err := git("checkout", "--quiet", "--detach", commit); err != nil {
		return nil, err
	}

	bundle, err := readBundle(ctx, os.DirFS(filepath.Join(repository, filepath.FromSlash(bundleSourceDir))))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", bundleSourceDir, err)
	}

	return bundle, nil
}
