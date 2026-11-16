package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

var human = Actor{Runtime: RuntimeHuman}

func newTestService(t *testing.T, root string) *Service {
	t.Helper()
	settings := NewSettings()
	settings.RootPath = root
	service, err := NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func readEvents(t *testing.T, root, id string) []Event {
	t.Helper()
	events, err := newStore(root).events(id)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func eventNames(events []Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Event)
	}
	return names
}

func TestEntryLifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		service := newTestService(t, root)
		codex := Actor{Runtime: "codex", Session: "s-2"}

		created, err := service.New(t.Context(), human, NewOptions{
			Title: "Move schemas",
			Tags:  []string{"Refactor", "agentenv"},
			Issue: "42",
		})
		if err != nil {
			t.Fatal(err)
		}
		id := created.Entry.ID
		if created.Path != ".go42x/journal/"+id || created.Entry.Status != StatusOpen ||
			created.Entry.ExpiresAt != nil || created.Entry.Refs.Issue != "42" ||
			!slices.Equal(created.Entry.Tags, []string{"refactor", "agentenv"}) {
			t.Fatalf("new entry = %+v", created)
		}
		notes, err := os.ReadFile(filepath.Join(root, created.Path, notesFile))
		if err != nil || !strings.HasPrefix(string(notes), "# Move schemas\n") ||
			!strings.Contains(string(notes), "## Next\n") {
			t.Fatalf("notes template = %q, %v", notes, err)
		}

		time.Sleep(time.Hour)
		noted, err := service.Note(t.Context(), codex, NoteOptions{ID: id, Summary: "Moved the files"})
		if err != nil || noted.Entry.Runtime != "codex" || noted.Entry.Session != "s-2" ||
			noted.Entry.Refs.Issue != "42" {
			t.Fatalf("note = %+v, %v", noted, err)
		}

		finished, err := service.Done(t.Context(), human, DoneOptions{ID: id, Summary: "Merged"})
		if err != nil {
			t.Fatal(err)
		}
		wantExpiry := time.Now().Add(DoneRetention)
		if finished.Entry.Status != StatusDone || finished.Entry.ExpiresAt == nil ||
			!finished.Entry.ExpiresAt.Equal(wantExpiry) ||
			!finished.Entry.UpdatedAt.Equal(time.Now()) {
			t.Fatalf("done = %+v", finished)
		}
		if _, err := service.Done(t.Context(), human, DoneOptions{ID: id}); err == nil {
			t.Fatal("finished entry finished again")
		}

		reopened, err := service.Note(t.Context(), human, NoteOptions{ID: id, Tags: []string{}})
		if err != nil || reopened.Entry.Status != StatusOpen || reopened.Entry.ExpiresAt != nil ||
			len(reopened.Entry.Tags) != 0 {
			t.Fatalf("reopen = %+v, %v", reopened, err)
		}
		abandoned, err := service.Done(t.Context(), human, DoneOptions{ID: id, Abandoned: true})
		if err != nil || abandoned.Entry.Status != StatusAbandoned ||
			!abandoned.Entry.ExpiresAt.Equal(time.Now().Add(AbandonedRetention)) {
			t.Fatalf("abandon = %+v, %v", abandoned, err)
		}

		shown, err := service.Show(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got := eventNames(
			shown.Events,
		); !slices.Equal(
			got,
			[]string{"created", "note", "done", "reopened", "abandoned"},
		) {
			t.Errorf("events = %q", got)
		}
		if shown.Events[1].Runtime != "codex" || shown.Events[1].Summary != "Moved the files" {
			t.Errorf("event details = %+v", shown.Events)
		}
		if len(shown.Files) != 3 ||
			!strings.HasPrefix(shown.Entry.Schema, "https://raw.githubusercontent.com/go42-dev/go42x/") {
			t.Errorf("show = %+v", shown)
		}
		leftovers, err := filepath.Glob(filepath.Join(root, created.Path, ".*.tmp-*"))
		if err != nil || len(leftovers) != 0 {
			t.Errorf("temporary files left behind: %v, %v", leftovers, err)
		}
	})
}

func TestGCRemovesOnlyExpiredFinishedEntries(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		service := newTestService(t, root)
		create := func(title string) string {
			t.Helper()
			result, err := service.New(t.Context(), human, NewOptions{Title: title})
			if err != nil {
				t.Fatal(err)
			}
			return result.Entry.ID
		}
		open, done, abandoned, kept := create("Open"), create("Done"), create("Abandoned"), create("Kept")
		for _, options := range map[string]DoneOptions{
			done:      {ID: done},
			abandoned: {ID: abandoned, Abandoned: true},
			kept:      {ID: kept, Keep: true},
		} {
			if _, err := service.Done(t.Context(), human, options); err != nil {
				t.Fatal(err)
			}
		}
		writeTestFile(t, filepath.Join(root, ".go42x/journal/no-record/file.txt"), "stray")
		ids := func(result *GCResult) []string {
			var ids []string
			for _, item := range result.Entries {
				ids = append(ids, item.ID)
			}
			slices.Sort(ids)
			return ids
		}

		time.Sleep(AbandonedRetention)
		report, err := service.GC(t.Context(), GCOptions{})
		if err != nil || !slices.Equal(ids(report), []string{abandoned}) {
			t.Fatalf("gc after abandoned retention = %+v, %v", report, err)
		}

		time.Sleep(DoneRetention)
		report, err = service.GC(t.Context(), GCOptions{})
		if err != nil || report.Applied ||
			!slices.Equal(ids(report), slices.Sorted(slices.Values([]string{abandoned, done}))) {
			t.Fatalf("gc report = %+v, %v", report, err)
		}
		if _, err := os.Stat(filepath.Join(root, ".go42x/journal", done)); err != nil {
			t.Fatalf("dry run removed an entry: %v", err)
		}

		removed, err := service.GC(t.Context(), GCOptions{Apply: true})
		if err != nil || !removed.Applied || len(removed.Entries) != 2 {
			t.Fatalf("gc apply = %+v, %v", removed, err)
		}
		for id, exists := range map[string]bool{open: true, kept: true, done: false, abandoned: false, "no-record": true} {
			if _, err := os.Stat(filepath.Join(root, ".go42x/journal", id)); (err == nil) != exists {
				t.Errorf("%s exists = %v, want %v", id, err == nil, exists)
			}
		}

		index, err := os.ReadFile(filepath.Join(root, ".go42x/journal", indexFile))
		if err != nil {
			t.Fatal(err)
		}
		var document indexDocument
		if err := json.Unmarshal(index, &document); err != nil || len(document.Entries) != 2 {
			t.Fatalf("index after gc = %s, %v", index, err)
		}
	})
}

func TestListFiltersAndStrayContent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		service := newTestService(t, root)
		first, err := service.New(t.Context(), human, NewOptions{Title: "First", Tags: []string{"fix", "kwb"}})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		second, err := service.New(
			t.Context(),
			Actor{Runtime: "codex"},
			NewOptions{Title: "Second", Tags: []string{"fix"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Done(t.Context(), human, DoneOptions{ID: first.Entry.ID}); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(root, ".go42x/journal/20261110-broken-0000/entry.json"), "{")
		writeTestFile(t, filepath.Join(root, ".go42x/journal/stray.txt"), "stray")
		writeTestFile(t, filepath.Join(root, ".build/old-task/report.md"), "old")
		writeTestFile(t, filepath.Join(root, ".build/coverage.out"), "old")
		writeTestFile(t, filepath.Join(root, ".build/reports/coverage.out"), "current")
		writeTestFile(t, filepath.Join(root, ".build/tmp/vendoring/x"), "current")
		// Interrupted writes and failed gc deletions leave hidden content behind.
		leftovers := []string{
			".go42x/journal/.index.json.tmp-123",
			".go42x/journal/" + first.Entry.ID + "/.entry.json.tmp-456",
			".go42x/journal/.trash-20261110-old-0000",
		}
		writeTestFile(t, filepath.Join(root, leftovers[0]), "partial")
		writeTestFile(t, filepath.Join(root, leftovers[1]), "partial")
		writeTestFile(t, filepath.Join(root, leftovers[2], notesFile), "remains")

		for name, tt := range map[string]struct {
			options ListOptions
			want    []string
		}{
			"all":       {ListOptions{}, []string{first.Entry.ID, second.Entry.ID}},
			"open":      {ListOptions{Status: StatusOpen}, []string{second.Entry.ID}},
			"every tag": {ListOptions{Tags: []string{"fix", "kwb"}}, []string{first.Entry.ID}},
		} {
			result, err := service.List(t.Context(), tt.options)
			if err != nil {
				t.Fatal(err)
			}

			var got []string
			for _, entry := range result.Entries {
				got = append(got, entry.ID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("%s: entries = %q, want %q", name, got, tt.want)
			}
			if len(result.Invalid) != 1 || result.Invalid[0].Path != ".go42x/journal/20261110-broken-0000" {
				t.Errorf("%s: invalid = %+v", name, result.Invalid)
			}
		}

		stray, err := service.List(t.Context(), ListOptions{Stray: true})
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, item := range stray.Stray {
			paths = append(paths, item.Path)
		}
		slices.Sort(paths)
		want := append([]string{".build/coverage.out", ".build/old-task", ".go42x/journal/stray.txt"}, leftovers...)
		slices.Sort(want)
		if !slices.Equal(paths, want) {
			t.Errorf("stray = %q, want %q", paths, want)
		}
		if _, err := service.List(t.Context(), ListOptions{Stray: true, Status: StatusOpen}); err == nil {
			t.Error("stray listing accepted entry filters")
		}
	})
}

func TestListWithoutEntriesEncodesEmptyArray(t *testing.T) {
	t.Parallel()
	result, err := newTestService(t, t.TempDir()).List(t.Context(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(data), `"entries":[]`) {
		t.Errorf("list without entries = %s, %v", data, err)
	}
}

func TestGCContinuesAfterFailedDeletion(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions must prevent deletion")
	}
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		service := newTestService(t, root)
		var ids []string
		for _, title := range []string{"Locked", "Free"} {
			created, err := service.New(t.Context(), human, NewOptions{Title: title})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Done(
				t.Context(),
				human,
				DoneOptions{ID: created.Entry.ID, Abandoned: true},
			); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, created.Entry.ID)
		}
		locked, free := ids[0], ids[1]
		// A read-only directory, such as a Go module cache, keeps its files from deletion.
		writeTestFile(t, filepath.Join(root, ".go42x/journal", locked, "cache/module.txt"), "read-only")
		if err := os.Chmod(filepath.Join(root, ".go42x/journal", locked, "cache"), 0500); err != nil {
			t.Fatal(err)
		}
		trash := ".go42x/journal/" + trashPrefix + locked
		t.Cleanup(func() {
			_ = os.Chmod(filepath.Join(root, trash, "cache"), 0700)
		})

		time.Sleep(AbandonedRetention)
		result, err := service.GC(t.Context(), GCOptions{Apply: true})
		if err == nil || !strings.Contains(err.Error(), trash) {
			t.Fatalf("gc error = %v, want the remains in %s", err, trash)
		}
		if len(result.Entries) != 1 || result.Entries[0].ID != free {
			t.Errorf("removed = %+v, want only %s", result.Entries, free)
		}

		index, err := os.ReadFile(filepath.Join(root, ".go42x/journal", indexFile))
		if err != nil {
			t.Fatal(err)
		}
		var document indexDocument
		if err := json.Unmarshal(index, &document); err != nil || len(document.Entries) != 0 {
			t.Errorf("index after failed gc = %s, %v", index, err)
		}
		stray, err := service.List(t.Context(), ListOptions{Stray: true})
		if err != nil || len(stray.Stray) != 1 || stray.Stray[0].Path != trash {
			t.Errorf("stray after failed gc = %+v, %v", stray, err)
		}
	})
}

func TestNewRemovesEntryAfterFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A directory in place of the index makes the last step of New fail.
	if err := os.MkdirAll(filepath.Join(root, ".go42x/journal", indexFile), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestService(t, root).New(t.Context(), human, NewOptions{Title: "Fails"}); err == nil {
		t.Fatal("new succeeded without writing the index")
	}

	children, err := os.ReadDir(filepath.Join(root, ".go42x/journal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		if child.Name() != indexFile && child.Name() != lockFile {
			t.Errorf("failed new left %s behind", child.Name())
		}
	}
}

func TestEventsAfterTornLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := newTestService(t, root)
	created, err := service.New(t.Context(), human, NewOptions{Title: "Torn log"})
	if err != nil {
		t.Fatal(err)
	}
	// An interrupted append leaves a line without its newline.
	file, err := os.OpenFile(filepath.Join(root, created.Path, logFile), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(`{"at":"2026-11-10`)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Note(t.Context(), human, NoteOptions{ID: created.Entry.ID, Summary: "After"}); err != nil {
		t.Fatal(err)
	}
	if got := eventNames(readEvents(t, root, created.Entry.ID)); !slices.Equal(got, []string{"created", "note"}) {
		t.Errorf("events after a torn line = %q", got)
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := newTestService(t, root)
	entry, err := service.New(t.Context(), human, NewOptions{Title: "Release notes", Tags: []string{"docs"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.New(t.Context(), human, NewOptions{Title: "Other task"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(
		t,
		filepath.Join(root, entry.Path, notesFile),
		"# Release notes\n\n## State\n\nWaiting for the Changelog review.\n",
	)

	for query, want := range map[string][]string{
		"release":   {entry.Entry.ID},
		"CHANGELOG": {entry.Entry.ID},
		"docs":      {entry.Entry.ID},
		"task":      {other.Entry.ID},
		"absent":    nil,
		// Template comments are shared by every entry.
		"concrete steps": nil,
	} {
		result, err := service.Search(t.Context(), SearchOptions{Query: query})
		if err != nil {
			t.Fatal(err)
		}

		var got []string
		for _, match := range result.Matches {
			got = append(got, match.Entry.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Search(%q) = %q, want %q", query, got, want)
		}
	}

	result, err := service.Search(t.Context(), SearchOptions{Query: "changelog"})
	if err != nil || len(result.Matches) != 1 ||
		!slices.Equal(result.Matches[0].Lines, []string{"Waiting for the Changelog review."}) {
		t.Fatalf("search lines = %+v, %v", result, err)
	}
	if _, err := service.Search(t.Context(), SearchOptions{Query: " "}); err == nil {
		t.Error("empty query accepted")
	}
}

func TestConcurrentChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := newTestService(t, root)
	shared, err := service.New(t.Context(), human, NewOptions{Title: "Shared"})
	if err != nil {
		t.Fatal(err)
	}

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, 2*writers)
	for i := range writers {
		wg.Go(func() {
			// Separate services share only the files, as separate processes do.
			other := newTestService(t, root)
			if _, err := other.New(t.Context(), human, NewOptions{Title: "Parallel"}); err != nil {
				errs <- err
			}
			if _, err := other.Note(
				t.Context(),
				human,
				NoteOptions{ID: shared.Entry.ID, Summary: string(rune('a' + i))},
			); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	listed, err := service.List(t.Context(), ListOptions{})
	if err != nil || len(listed.Entries) != writers+1 || len(listed.Invalid) != 0 {
		t.Fatalf("entries after concurrent writes = %+v, %v", listed, err)
	}
	if events := readEvents(t, root, shared.Entry.ID); len(events) != writers+1 {
		t.Errorf("shared entry log has %d events, want %d", len(events), writers+1)
	}
	index, err := os.ReadFile(filepath.Join(root, ".go42x/journal", indexFile))
	if err != nil {
		t.Fatal(err)
	}
	var document indexDocument
	if err := json.Unmarshal(index, &document); err != nil || len(document.Entries) != writers+1 {
		t.Errorf("index lists %d entries, want %d: %v", len(document.Entries), writers+1, err)
	}
}

func TestRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := newTestService(t, root)
	writeTestFile(t, filepath.Join(root, "secret.txt"), "outside")

	for name, call := range map[string]func() error{
		"traversing show": func() error {
			_, err := service.Show(t.Context(), "../../secret.txt")
			return err
		},
		"missing entry": func() error {
			_, err := service.Show(t.Context(), "20261110-missing-0000")
			return err
		},
		"blank title": func() error {
			_, err := service.New(t.Context(), human, NewOptions{Title: " "})
			return err
		},
		"unknown runtime": func() error {
			_, err := service.New(t.Context(), Actor{Runtime: "robot"}, NewOptions{Title: "Task"})
			return err
		},
		"invalid tag": func() error {
			_, err := service.New(t.Context(), human, NewOptions{Title: "Task", Tags: []string{"Not a tag"}})
			return err
		},
		"unknown status filter": func() error {
			_, err := service.List(t.Context(), ListOptions{Status: "paused"})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRefsLogGitFailures(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required")
	}
	var logs bytes.Buffer
	settings := NewSettings()
	settings.RootPath = t.TempDir()
	service, err := NewService(settings, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}

	entry, err := service.New(t.Context(), human, NewOptions{Title: "No repository"})
	if err != nil || len(entry.Entry.Refs.Branch) > 0 || len(entry.Entry.Refs.Commit) > 0 {
		t.Fatalf("new outside a repository = %+v, %v", entry, err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "git rev-parse") {
		t.Errorf("Git failure was not logged: %s", logs.String())
	}
}

func TestRefsRecordTheCheckout(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"-c", "user.name=go42x", "-c", "user.email=go42x@example.com", "-c", "commit.gpgsign=false",
			"commit", "--quiet", "--allow-empty", "--message", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}

	entry, err := newTestService(t, root).New(t.Context(), human, NewOptions{Title: "Refs"})
	if err != nil {
		t.Fatal(err)
	}

	if entry.Entry.Refs.Branch != "main" || !commitPattern.MatchString(entry.Entry.Refs.Commit) {
		t.Errorf("refs = %+v", entry.Entry.Refs)
	}
}
