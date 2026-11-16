package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type journalEntry struct {
	Entry struct {
		ID        string   `json:"id"`
		Status    string   `json:"status"`
		Tags      []string `json:"tags"`
		Runtime   string   `json:"runtime"`
		Session   string   `json:"session"`
		ExpiresAt *string  `json:"expires_at"`
		Refs      struct {
			PR    string `json:"pr"`
			Issue string `json:"issue"`
		} `json:"refs"`
	} `json:"entry"`
	Path string `json:"path"`
}

func decodeJournal[T any](t *testing.T, stdout string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		t.Fatalf("journal output is not JSON: %v\n%s", err, stdout)
	}
	return value
}

func TestJournalCommands(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)

	for _, args := range [][]string{
		{"agentenv", "journal", "new"},
		{"agentenv", "journal", "new", "--title", "Task", "--tag", "Not_A_Tag"},
		{"agentenv", "journal", "new", "--title", "Task", "--runtime", "robot"},
		{"agentenv", "journal", "show", "../outside"},
		{"agentenv", "journal", "list", "--stray", "--status", "open"},
		{"agentenv", "journal", "list", "--runtime", "codex"},
		{"agentenv", "journal", "search", " "},
	} {
		p.run(t, 2, args...)
	}

	created := decodeJournal[journalEntry](t, p.run(
		t,
		0,
		"agentenv",
		"journal",
		"new",
		"--title",
		"E2E task",
		"--tag",
		"test,journal",
		"--runtime",
		"human",
		"--json",
	).stdout)
	id := created.Entry.ID
	if created.Entry.Status != "open" || created.Entry.Runtime != "human" || created.Path != ".go42x/journal/"+id ||
		strings.Join(created.Entry.Tags, ",") != "test,journal" {
		t.Fatalf("created = %+v", created)
	}

	listed := p.run(t, 0, "agentenv", "journal", "list", "--status", "open").stdout
	if !strings.Contains(listed, id+"  open") || !strings.Contains(listed, "E2E task  [test, journal]") {
		t.Errorf("list = %s", listed)
	}

	if shown := p.run(t, 0, "agentenv", "journal", "show", id).stdout; !strings.Contains(shown, "Status:  open") ||
		!strings.Contains(shown, "Notes:\n# E2E task") {
		t.Errorf("show = %s", shown)
	}

	finished := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "done", id, "--summary", "Finished", "--runtime", "codex", "--json").stdout)
	if finished.Entry.Status != "done" || finished.Entry.ExpiresAt == nil || finished.Entry.Runtime != "codex" {
		t.Fatalf("done = %+v", finished)
	}

	if found := p.run(t, 0, "agentenv", "journal", "search", "e2e").stdout; !strings.Contains(found, id) {
		t.Errorf("search = %s", found)
	}
	if gc := p.run(t, 0, "agentenv", "journal", "gc").stdout; gc != "No expired journal entries.\n" {
		t.Errorf("gc = %q", gc)
	}

	p.write(t, ".build/old-task/report.md", "Old report\n")
	p.write(t, ".build/reports/coverage.out", "mode: atomic\n")
	stray := p.run(t, 0, "agentenv", "journal", "list", "--stray").stdout
	if !strings.Contains(stray, ".build/old-task") || strings.Contains(stray, ".build/reports") {
		t.Errorf("stray = %s", stray)
	}
	if index := p.run(
		t,
		0,
		"agentenv",
		"journal",
		"index",
	).stdout; !strings.Contains(
		index,
		"Indexed 1 journal entries",
	) {
		t.Errorf("index = %s", index)
	}
}

func TestJournalEntryFlags(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)

	empty := decodeJournal[struct {
		Entries []json.RawMessage `json:"entries"`
	}](t, p.run(t, 0, "agentenv", "journal", "list", "--json").stdout)
	if empty.Entries == nil {
		t.Error("list without entries reports null entries")
	}

	created := decodeJournal[journalEntry](t, p.run(t, 0, "agentenv", "journal", "new",
		"--title", "Flags", "--tag", "fix", "--pr", "12", "--issue", "34", "--runtime", "human", "--json").stdout)
	id := created.Entry.ID
	if created.Entry.Refs.PR != "12" || created.Entry.Refs.Issue != "34" {
		t.Fatalf("created = %+v", created.Entry)
	}
	noted := decodeJournal[journalEntry](t, p.run(t, 0, "agentenv", "journal", "note", id,
		"--tag", "", "--pr", "56", "--summary", "Cleared the tags", "--runtime", "human", "--json").stdout)
	if len(noted.Entry.Tags) != 0 || noted.Entry.Refs.PR != "56" || noted.Entry.Refs.Issue != "34" {
		t.Errorf("noted = %+v", noted.Entry)
	}

	kept := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "done", id, "--keep", "--runtime", "human", "--json").stdout)
	if kept.Entry.Status != "done" || kept.Entry.ExpiresAt != nil {
		t.Errorf("kept = %+v", kept.Entry)
	}
	p.run(t, 0, "agentenv", "journal", "note", id, "--summary", "Reopened", "--runtime", "human")
	abandoned := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "done", id, "--abandoned", "--runtime", "human", "--json").stdout)
	if abandoned.Entry.Status != "abandoned" || abandoned.Entry.ExpiresAt == nil {
		t.Errorf("abandoned = %+v", abandoned.Entry)
	}

	p.run(t, 0, "agentenv", "journal", "new", "--title", "Flags again", "--runtime", "human")
	for limit, want := range map[string]int{"1": 1, "0": 2} {
		found := decodeJournal[struct {
			Matches []json.RawMessage `json:"matches"`
		}](t, p.run(t, 0, "agentenv", "journal", "search", "flags", "--limit", limit, "--json").stdout)
		if len(found.Matches) != want {
			t.Errorf("search --limit %s found %d entries, want %d", limit, len(found.Matches), want)
		}
	}
}

func TestJournalActorDetection(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.env = append(p.env, "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=s-1")

	detected := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "new", "--title", "Detected", "--json").stdout)
	if detected.Entry.Runtime != "claude" || detected.Entry.Session != "s-1" {
		t.Errorf("detected actor = %+v", detected.Entry)
	}
	fromFlags := decodeJournal[journalEntry](t, p.run(
		t,
		0,
		"agentenv",
		"journal",
		"new",
		"--title",
		"From flags",
		"--runtime",
		"human",
		"--session",
		"s-2",
		"--json",
	).stdout)
	if fromFlags.Entry.Runtime != "human" || fromFlags.Entry.Session != "s-2" {
		t.Errorf("flags did not override detection: %+v", fromFlags.Entry)
	}
}

func TestAgentEnvInitIgnoresJournalAndBuildDirectories(t *testing.T) {
	t.Parallel()
	p := newProject(t)

	p.run(t, 0, "agentenv", "init")

	gitignore := p.read(t, ".gitignore")
	if !strings.Contains(gitignore, "\n.go42x/journal/\n.build/\n") {
		t.Errorf("gitignore does not ignore the journal and build directories:\n%s", gitignore)
	}
	p.run(t, 0, "kwb", "build")
	if kwbIgnore := p.read(t, ".go42x/kwb.ignore"); !strings.Contains(kwbIgnore, ".go42x/journal/**\n") {
		t.Errorf("kwb.ignore does not exclude the journal:\n%s", kwbIgnore)
	}
	for _, schema := range []string{"go42x.schema.yaml", "journal-entry.schema.json"} {
		if _, err := os.Stat(filepath.Join(p.root, ".go42x/schemas", schema)); err != nil {
			t.Errorf("init did not install schema %s: %v", schema, err)
		}
	}
}
