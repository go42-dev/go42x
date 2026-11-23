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
		ID      string   `json:"id"`
		Tags    []string `json:"tags"`
		Keep    bool     `json:"keep"`
		Runtime string   `json:"runtime"`
		Session string   `json:"session"`
		Refs    struct {
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
		{"agentenv", "journal", "new", "--title", "Task", "--runtime", "Robot Agent"},
		{"agentenv", "journal", "show", "../outside"},
		{"agentenv", "journal", "list", "--stray", "--kept"},
		{"agentenv", "journal", "list", "--status", "open"},
		{"agentenv", "journal", "done", "20261110-task-0000"},
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
	if created.Entry.Keep || created.Entry.Runtime != "human" || created.Path != ".go42x/journal/"+id ||
		strings.Join(created.Entry.Tags, ",") != "test,journal" {
		t.Fatalf("created = %+v", created)
	}

	listed := p.run(t, 0, "agentenv", "journal", "list").stdout
	if !strings.Contains(listed, id+"  ") || !strings.Contains(listed, "E2E task  [test, journal]\n") {
		t.Errorf("list = %s", listed)
	}

	if shown := p.run(t, 0, "agentenv", "journal", "show", id).stdout; !strings.Contains(shown, "Kept:    no") ||
		!strings.Contains(shown, "Notes:\n# E2E task") {
		t.Errorf("show = %s", shown)
	}

	noted := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "note", id, "--summary", "Finished", "--runtime", "codex", "--json").stdout)
	if noted.Entry.Keep || noted.Entry.Runtime != "codex" {
		t.Fatalf("note = %+v", noted)
	}

	if found := p.run(t, 0, "agentenv", "journal", "search", "e2e").stdout; !strings.Contains(found, id) {
		t.Errorf("search = %s", found)
	}
	if gc := p.run(t, 0, "agentenv", "journal", "gc").stdout; gc != "No expired journal entries.\n" {
		t.Errorf("gc = %q", gc)
	}

	p.write(t, ".go42x/journal/old-task/report.md", "Old report\n")
	p.write(t, ".build/old-task/report.md", "Build output\n")
	stray := p.run(t, 0, "agentenv", "journal", "list", "--stray").stdout
	if !strings.Contains(stray, ".go42x/journal/old-task") || strings.Contains(stray, ".build") {
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
		"--title", "Flags", "--tag", "fix", "--pr", "12", "--issue", "https://github.com/example/project/issues/34",
		"--runtime", "human", "--json").stdout)
	id := created.Entry.ID
	if created.Entry.Refs.PR != "12" || created.Entry.Refs.Issue != "https://github.com/example/project/issues/34" {
		t.Fatalf("created = %+v", created.Entry)
	}
	noted := decodeJournal[journalEntry](t, p.run(t, 0, "agentenv", "journal", "note", id,
		"--tag", "", "--pr", "56", "--summary", "Cleared the tags", "--runtime", "human", "--json").stdout)
	if len(noted.Entry.Tags) != 0 || noted.Entry.Refs.PR != "56" ||
		noted.Entry.Refs.Issue != "https://github.com/example/project/issues/34" {
		t.Errorf("noted = %+v", noted.Entry)
	}

	kept := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "note", id, "--keep", "--runtime", "human", "--json").stdout)
	if !kept.Entry.Keep {
		t.Errorf("kept = %+v", kept.Entry)
	}
	for args, want := range map[string]int{
		"--kept": 1, "--issue=34": 1, "--issue=#34": 1, "--issue=4": 0, "--pr=56": 1,
		"--since=2999-01-01T00:00:00Z": 0,
	} {
		listed := decodeJournal[struct {
			Entries []json.RawMessage `json:"entries"`
		}](t, p.run(t, 0, "agentenv", "journal", "list", args, "--json").stdout)
		if len(listed.Entries) != want {
			t.Errorf("list %s found %d entries, want %d", args, len(listed.Entries), want)
		}
	}
	p.run(t, 2, "agentenv", "journal", "list", "--since", "yesterday")

	p.write(t, ".go42x/journal/"+id+"/notes.md", "# Flags\n\n## Goal\n\n<!-- Guidance. -->\n\n## Next\n\nShip.\n")
	shown := decodeJournal[struct {
		Notes    *string `json:"notes"`
		Sections []struct {
			Heading string `json:"heading"`
			Text    string `json:"text"`
		} `json:"sections"`
		Events []json.RawMessage `json:"events"`
	}](t, p.run(t, 0,
		"agentenv", "journal", "show", id, "--sections", "--since", "2999-01-01T00:00:00Z", "--json").stdout)
	if shown.Notes != nil || len(shown.Sections) != 1 || shown.Sections[0].Heading != "Next" ||
		shown.Sections[0].Text != "Ship." || len(shown.Events) != 0 {
		t.Errorf("show --sections --since = %+v", shown)
	}

	p.write(t, "review.md", "Review\n")
	attached := p.run(t, 0, "agentenv", "journal", "attach", id, "review.md", "--name", "review-1.md").stdout
	if attached != "Attached .go42x/journal/"+id+"/review-1.md (7 B); record the change with journal note.\n" {
		t.Errorf("attach = %q", attached)
	}
	p.run(t, 1, "agentenv", "journal", "attach", id, "review.md", "--name", "review-1.md")
	p.run(t, 2, "agentenv", "journal", "attach", id, "review.md", "--name", "notes.md")
	released := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "note", id, "--keep=false", "--runtime", "human", "--json").stdout)
	if released.Entry.Keep {
		t.Errorf("--keep=false = %+v", released.Entry)
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

func TestAgentEnvInitIgnoresJournalAndTmpDirectories(t *testing.T) {
	t.Parallel()
	p := newProject(t)

	p.run(t, 0, "agentenv", "init")

	if gitignore := p.read(t, ".go42x/.gitignore"); !strings.Contains(gitignore, "\n/journal/\n") ||
		!strings.Contains(gitignore, "\n/tmp/\n") {
		t.Errorf(".go42x/.gitignore does not ignore the journal and temporary directories:\n%s", gitignore)
	}
	// Indexing honors .go42x/.gitignore, which follows the configured journal path.
	p.run(t, 0, "agentenv", "journal", "new", "--title", "Indexed notes", "--runtime", "human")
	p.run(t, 0, "kwb", "build")
	if found := p.run(
		t,
		0,
		"kwb",
		"search",
		"Indexed notes",
		"--json",
	).stdout; strings.Contains(
		found,
		".go42x/journal",
	) {
		t.Errorf("knowledge base indexed the journal:\n%s", found)
	}
	for _, schema := range []string{"go42x.schema.yaml", "journal-entry.schema.json"} {
		if _, err := os.Stat(filepath.Join(p.root, ".go42x/schemas", schema)); err != nil {
			t.Errorf("init did not install schema %s: %v", schema, err)
		}
	}
}

func TestAgentEnvIgnoresConfiguredTmpDirectory(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, ".go42x/go42x.local.yaml", "context: {tmp-dir: scratch}\n")

	p.run(t, 0, "agentenv", "init")

	if gitignore := p.read(t, ".go42x/.gitignore"); !strings.Contains(gitignore, "\n/scratch/\n") ||
		strings.Contains(gitignore, "\n/tmp/\n") {
		t.Errorf("init did not ignore the configured temporary directory:\n%s", gitignore)
	}
	p.write(t, ".go42x/go42x.local.yaml", "context: {tmp-dir: state/tmp}\n")
	p.run(t, 0, "agentenv", "generate", "--providers=")
	if gitignore := p.read(t, ".go42x/.gitignore"); !strings.Contains(gitignore, "\n/state/tmp/\n") ||
		strings.Contains(gitignore, "\n/scratch/\n") {
		t.Errorf("generate did not ignore the changed temporary directory:\n%s", gitignore)
	}
	for _, invalid := range []string{"../outside", "/tmp/go42x"} {
		p.write(t, ".go42x/go42x.local.yaml", "context: {tmp-dir: "+invalid+"}\n")
		p.run(t, 1, "agentenv", "generate", "--providers=")
	}
}

func TestJournalConfiguredPath(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, ".go42x/go42x.local.yaml", "journal: {path: state/journal, retention-days: 7}\n")

	p.run(t, 0, "agentenv", "init")

	if gitignore := p.read(t, ".go42x/.gitignore"); !strings.Contains(gitignore, "\n/state/journal/\n") ||
		strings.Contains(gitignore, "\n/journal/\n") {
		t.Errorf(".go42x/.gitignore does not ignore the configured journal:\n%s", gitignore)
	}
	created := decodeJournal[journalEntry](t, p.run(t, 0,
		"agentenv", "journal", "new", "--title", "Configured", "--runtime", "human", "--json").stdout)
	if created.Path != ".go42x/state/journal/"+created.Entry.ID {
		t.Errorf("created in %s, want .go42x/state/journal", created.Path)
	}

	p.write(t, ".go42x/go42x.local.yaml", "journal: {retention-days: 0}\n")
	p.run(t, 1, "agentenv", "journal", "list")
}
