package journal

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go42-dev/go42x/assets"
)

func TestNormalizeTags(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		tags  []string
		want  []string
		valid bool
	}{
		{
			name:  "none",
			tags:  nil,
			want:  []string{},
			valid: true,
		},
		{
			name:  "lowercased",
			tags:  []string{"Refactor", " agentenv "},
			want:  []string{"refactor", "agentenv"},
			valid: true,
		},
		{
			name: "invalid characters",
			tags: []string{"fix_bug"},
		},
		{
			name: "leading hyphen",
			tags: []string{"-fix"},
		},
		{
			name: "too long",
			tags: []string{strings.Repeat("a", 33)},
		},
		{
			name: "duplicate",
			tags: []string{"fix", "FIX"},
		},
		{
			name: "too many",
			tags: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeTags(tt.tags)

			if (err == nil) != tt.valid {
				t.Fatalf("NormalizeTags() error = %v, want valid %v", err, tt.valid)
			}
			if tt.valid && !slices.Equal(got, tt.want) {
				t.Errorf("NormalizeTags() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateID(t *testing.T) {
	t.Parallel()
	for id, valid := range map[string]bool{
		"20261110-move-schemas-3f2a":  true,
		"20261110-entry-0000":         true,
		"":                            false,
		"../20261110-entry-0000":      false,
		"20261110-entry-0000/../x":    false,
		"20261110-Entry-0000":         false,
		"2026111-entry-0000":          false,
		"20261110-entry-000g":         false,
		"20261110--double-0000":       false,
		"20261110-entry-0000.json":    false,
		"20261110-" + "a-0000" + "\n": false,
	} {
		if err := ValidateID(id); (err == nil) != valid {
			t.Errorf("ValidateID(%q) = %v, want valid %v", id, err, valid)
		}
	}
}

func TestNewID(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 10, 23, 30, 0, 0, time.FixedZone("east", 3*60*60))
	for title, prefix := range map[string]string{
		"Move schemas into the bundle!":   "20261110-move-schemas-into-the-bundle-",
		"  ??? ":                          "20261110-entry-",
		strings.Repeat("long title ", 10): "20261110-long-title-long-title-long-title-long-",
	} {
		id, err := newID(created, title)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.HasPrefix(id, prefix) || ValidateID(id) != nil {
			t.Errorf("newID(%q) = %q, want prefix %q and a valid ID", title, id, prefix)
		}
	}
}

func TestLimitsCountCharacters(t *testing.T) {
	t.Parallel()
	title := strings.Repeat("ж", maxTitleLength)
	if _, err := validateTitle(title); err != nil {
		t.Errorf("title of %d characters rejected: %v", maxTitleLength, err)
	}
	if _, err := validateTitle(title + "ж"); err == nil {
		t.Errorf("title of %d characters accepted", maxTitleLength+1)
	}
	summary := strings.Repeat("ж", 1000)
	if err := validateSummary(summary); err != nil {
		t.Errorf("summary of 1000 characters rejected: %v", err)
	}
	if err := validateSummary(summary + "ж"); err == nil {
		t.Error("summary of 1001 characters accepted")
	}
	if err := (Actor{Runtime: RuntimeHuman, Session: strings.Repeat("ж", 200)}).Validate(); err != nil {
		t.Errorf("session of 200 characters rejected: %v", err)
	}
	// The schema counts characters too, so the record accepts the longest title.
	if _, err := encodeEntry(&Entry{
		Version:   entryVersion,
		ID:        "20261110-entry-0000",
		Title:     title,
		Status:    StatusOpen,
		Tags:      []string{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Runtime:   RuntimeHuman,
	}); err != nil {
		t.Errorf("record with a title of %d characters rejected: %v", maxTitleLength, err)
	}
}

// TestSchemaMatchesRules keeps the editor schema and the rules go42x enforces in step.
func TestSchemaMatchesRules(t *testing.T) {
	t.Parallel()
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(bundle, schemaPath)
	if err != nil {
		t.Fatal(err)
	}

	var schema struct {
		Properties struct {
			Runtime struct {
				Enum []string `json:"enum"`
			} `json:"runtime"`
			Tags struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					Pattern string `json:"pattern"`
				} `json:"items"`
			} `json:"tags"`
			Title struct {
				MaxLength int `json:"maxLength"`
			} `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(schema.Properties.Runtime.Enum, Runtimes()) {
		t.Errorf("schema runtimes = %q, want %q", schema.Properties.Runtime.Enum, Runtimes())
	}
	if schema.Properties.Tags.Items.Pattern != tagPattern.String() || schema.Properties.Tags.MaxItems != maxTags {
		t.Errorf("schema tags = %+v, want pattern %q and at most %d", schema.Properties.Tags, tagPattern, maxTags)
	}
	if schema.Properties.Title.MaxLength != maxTitleLength {
		t.Errorf("schema title length = %d, want %d", schema.Properties.Title.MaxLength, maxTitleLength)
	}
}

func TestDecodeEntryRejectsInvalidRecords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	valid := Entry{
		Version:   entryVersion,
		ID:        "20261110-entry-0000",
		Title:     "Entry",
		Status:    StatusOpen,
		Tags:      []string{},
		CreatedAt: now,
		UpdatedAt: now,
		Runtime:   RuntimeHuman,
	}
	data, err := encodeEntry(&valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeEntry(data, valid.ID); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}

	for name, edit := range map[string]func(map[string]any){
		"unknown field":      func(m map[string]any) { m["kind"] = "task" },
		"missing expiry":     func(m map[string]any) { delete(m, "expires_at") },
		"other version":      func(m map[string]any) { m["version"] = 2 },
		"unknown status":     func(m map[string]any) { m["status"] = "paused" },
		"unknown runtime":    func(m map[string]any) { m["runtime"] = "robot" },
		"invalid tag":        func(m map[string]any) { m["tags"] = []string{"Bad Tag"} },
		"directory mismatch": func(m map[string]any) { m["id"] = "20261110-other-0000" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			edit(document)
			changed, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := decodeEntry(changed, valid.ID); err == nil {
				t.Error("invalid entry accepted")
			}
		})
	}
}

func TestDetectActor(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		runtime  string
		env      map[string]string
		terminal bool
		want     Actor
	}{
		{
			name:    "explicit",
			runtime: "codex",
			env:     map[string]string{"CLAUDECODE": "1"},
			want:    Actor{Runtime: "codex"},
		},
		{
			name: "agent variable",
			env:  map[string]string{"AI_AGENT": "claude-code_2-1-311_agent", "CLAUDE_CODE_SESSION_ID": "c-1"},
			want: Actor{Runtime: "claude", Session: "c-1"},
		},
		{
			name: "Claude Code marker",
			env:  map[string]string{"CLAUDECODE": "1"},
			want: Actor{Runtime: "claude"},
		},
		{
			name: "unknown agent falls back to markers",
			env:  map[string]string{"AI_AGENT": "robot_1_agent", "CLAUDECODE": "1"},
			want: Actor{Runtime: "claude"},
		},
		{
			name:     "terminal",
			terminal: true,
			want:     Actor{Runtime: RuntimeHuman},
		},
		{
			name: "nothing known",
			want: Actor{Runtime: RuntimeUnknown},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(name string) (string, bool) {
				value, found := tt.env[name]
				return value, found
			}

			if got := DetectActor(tt.runtime, "", lookup, tt.terminal); got != tt.want {
				t.Errorf("DetectActor() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRuntimeFromClient(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"claude-code":      "claude",
		"codex-mcp-client": "codex",
		"GitHub Copilot":   "copilot",
		"antigravity":      "antigravity",
		"gemini-cli":       "antigravity",
		"crush":            "crush",
		"gh-aw":            "gh-aw",
		"":                 RuntimeUnknown,
		"inspector":        RuntimeUnknown,
	} {
		if got := RuntimeFromClient(name); got != want {
			t.Errorf("RuntimeFromClient(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNoteSections(t *testing.T) {
	t.Parallel()
	template, err := renderNotes("Task")
	if err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		notes string
		want  []NoteSection
	}{
		"template":    {string(template), nil},
		"no sections": {"# Task\n", nil},
		"filled": {
			"# Task\n\n## Goal\n\n<!-- What the task must achieve. -->\nShip it.\n\n" +
				"## Next\n<!--\nmultiline\n-->\n\n1. Test.\n2. Merge.\n\n### Later\n\nRelease.\n\n## Results\n\n",
			[]NoteSection{
				{Heading: "Goal", Text: "Ship it."},
				{Heading: "Next", Text: "1. Test.\n2. Merge.\n\n### Later\n\nRelease."},
			},
		},
		"text before the first section": {
			"# Task\n\nContext first.\n\n## State\n\nDone.\n",
			[]NoteSection{{Text: "Context first."}, {Heading: "State", Text: "Done."}},
		},
		"headings in fenced code": {
			"# Task\n\n## Results\n\n````markdown\n```text\n## Not a section\n```\n## Still not\n````\n\n" +
				"~~~\n# Not a title\n~~~\n",
			[]NoteSection{{
				Heading: "Results",
				Text:    "````markdown\n```text\n## Not a section\n```\n## Still not\n````\n\n~~~\n# Not a title\n~~~",
			}},
		},
	} {
		if got := noteSections(tt.notes); !slices.Equal(got, tt.want) {
			t.Errorf("%s: noteSections = %+v, want %+v", name, got, tt.want)
		}
	}
}
