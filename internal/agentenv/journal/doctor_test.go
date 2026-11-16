package journal

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go42-dev/go42x/internal/doctor/check"
)

func runCheck(t *testing.T, root, id string) check.Result {
	t.Helper()
	for _, diagnostic := range DoctorChecks(root) {
		if diagnostic.ID == id {
			return diagnostic.Run(t.Context())
		}
	}

	t.Fatalf("check %s is not registered", id)
	return check.Result{}
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required")
	}

	cmd := exec.Command("git", "-C", root, "init", "--quiet")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func TestDoctorChecksRegistration(t *testing.T) {
	t.Parallel()
	var ids []string
	for _, diagnostic := range DoctorChecks(t.TempDir()) {
		ids = append(ids, diagnostic.ID)
	}

	if !slices.Equal(ids, []string{
		"journal.location", "journal.entries", "journal.stale", "journal.gc",
		"journal.stray", "journal.index",
	}) {
		t.Errorf("checks = %q", ids)
	}
}

func TestDoctorLocation(t *testing.T) {
	t.Parallel()
	t.Run("outside Git", func(t *testing.T) {
		t.Parallel()
		if result := runCheck(t, t.TempDir(), "journal.location"); result.Status != check.StatusSkipped {
			t.Errorf("location = %+v", result)
		}
	})

	t.Run("not ignored", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		gitInit(t, root)

		result := runCheck(t, root, "journal.location")

		if result.Status != check.StatusFail || !slices.Equal(result.Evidence, []string{".go42x/journal/", ".build/"}) {
			t.Errorf("location = %+v", result)
		}
	})

	t.Run("ignored", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		gitInit(t, root)
		writeTestFile(t, filepath.Join(root, ".gitignore"), ".go42x/journal/\n.build/\n")

		if result := runCheck(t, root, "journal.location"); result.Status != check.StatusPass {
			t.Errorf("location = %+v", result)
		}
	})
}

func TestDoctorEntriesAndStrayContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, id := range []string{"journal.entries", "journal.stray"} {
		if result := runCheck(t, root, id); result.Status != check.StatusPass {
			t.Errorf("%s on an empty project = %+v", id, result)
		}
	}

	entry, err := newTestService(t, root).New(t.Context(), human, NewOptions{Title: "Credentials"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, entry.Path, ".env"), "TOKEN=x")
	writeTestFile(t, filepath.Join(root, ".go42x/journal/20261110-broken-0000/entry.json"), "{}")
	writeTestFile(t, filepath.Join(root, ".build/old-task/notes.md"), "old notes")
	writeTestFile(t, filepath.Join(root, ".build/reports/coverage.out"), "report")

	entries := runCheck(t, root, "journal.entries")
	if entries.Status != check.StatusWarn || len(entries.Evidence) != 2 {
		t.Errorf("entries = %+v", entries)
	}
	stray := runCheck(t, root, "journal.stray")
	if stray.Status != check.StatusWarn || !slices.Equal(stray.Evidence, []string{".build/old-task"}) {
		t.Errorf("stray = %+v", stray)
	}
}

func TestDoctorStaleAndExpiredEntries(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		service := newTestService(t, root)
		open, err := service.New(t.Context(), human, NewOptions{Title: "Open"})
		if err != nil {
			t.Fatal(err)
		}
		finished, err := service.New(t.Context(), human, NewOptions{Title: "Finished"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Done(t.Context(), human, DoneOptions{ID: finished.Entry.ID}); err != nil {
			t.Fatal(err)
		}
		s := newStore(root)

		if result := s.checkGC(t.Context()); result.Status != check.StatusPass {
			t.Errorf("gc before expiry = %+v", result)
		}

		time.Sleep(time.Hour)
		if result := s.checkStale(t.Context()); result.Status != check.StatusPass {
			t.Errorf("stale on a fresh entry = %+v", result)
		}
		time.Sleep(StaleAfter)
		stale := s.checkStale(t.Context())
		if stale.Status != check.StatusWarn || len(stale.Evidence) != 1 ||
			!strings.HasPrefix(stale.Evidence[0], open.Entry.ID) {
			t.Errorf("stale = %+v", stale)
		}

		time.Sleep(DoneRetention)
		expired := s.checkGC(t.Context())
		if expired.Status != check.StatusWarn || !slices.Equal(expired.Evidence, []string{finished.Entry.ID}) {
			t.Errorf("gc = %+v", expired)
		}
	})
}

func TestDoctorIndex(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if result := runCheck(t, root, "journal.index"); result.Status != check.StatusPass {
		t.Errorf("index without a journal = %+v", result)
	}

	service := newTestService(t, root)
	entry, err := service.New(t.Context(), human, NewOptions{Title: "Indexed"})
	if err != nil {
		t.Fatal(err)
	}
	if result := runCheck(t, root, "journal.index"); result.Status != check.StatusPass {
		t.Errorf("index after a change = %+v", result)
	}

	if err := os.RemoveAll(filepath.Join(root, entry.Path)); err != nil {
		t.Fatal(err)
	}
	if result := runCheck(t, root, "journal.index"); result.Status != check.StatusWarn {
		t.Errorf("index after removing an entry by hand = %+v", result)
	}
	if _, err := service.Index(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result := runCheck(t, root, "journal.index"); result.Status != check.StatusPass {
		t.Errorf("index after rebuilding = %+v", result)
	}
}

func TestFormatSize(t *testing.T) {
	t.Parallel()
	for size, want := range map[int64]string{
		0:                       "0 B",
		1023:                    "1023 B",
		1024:                    "1.0 KiB",
		8 * 1024 * 1024 * 1024:  "8.0 GiB",
		1536 * 1024 * 1024 * 10: "15.0 GiB",
	} {
		if got := FormatSize(size); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", size, got, want)
		}
	}
}
