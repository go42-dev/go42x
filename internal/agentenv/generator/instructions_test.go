package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/config"
)

func TestInstructionSizes(t *testing.T) {
	for _, tt := range []struct {
		engine string
		limit  int
		policy bool
	}{
		{
			engine: "codex",
			limit:  32768,
		},
		{
			engine: "claude",
			limit:  4194304,
		},
		{
			engine: "antigravity",
			limit:  24000,
		},
		{
			engine: "copilot",
			limit:  24000,
			policy: true,
		},
		{
			engine: "crush",
			limit:  24000,
			policy: true,
		},
	} {
		t.Run(tt.engine, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CODEX_HOME", t.TempDir())
			cfg := &config.Config{Providers: map[string]config.Provider{tt.engine: {}}}
			for _, extra := range []int{0, 1} {
				content := []byte(strings.Repeat("x", tt.limit+extra))

				checks, err := checkInstructions(t.Context(), root, cfg, content)

				if err != nil || len(checks) != 1 {
					t.Fatalf("checks = %v, %v", checks, err)
				}
				check := checks[0]
				if check.engine != tt.engine || check.bytes != len(content) || check.limitBytes != tt.limit {
					t.Fatalf("unexpected check: %+v", check)
				}
				if (len(check.warning) > 0) != (extra > 0) {
					t.Fatalf("warning at size %d: %q", len(content), check.warning)
				}
				if (check.limitSource == "project policy") != tt.policy {
					t.Fatalf("unexpected limit source: %+v", check)
				}
			}
		})
	}
}

func TestInstructionImports(t *testing.T) {
	for _, tt := range []struct {
		name    string
		engine  string
		content string
		files   map[string]string
		want    int
		failure string
	}{
		{
			name:    "UTF-8 bytes after expansion",
			engine:  "antigravity",
			content: "@[rules](rules.md)",
			files:   map[string]string{"rules.md": "éé"},
			want:    4,
		},
		{
			name:    "recursive Copilot imports",
			engine:  "copilot",
			content: "@rules.md",
			files: map[string]string{
				"rules.md":  "@nested.md",
				"nested.md": "rules",
			},
			want: 5,
		},
		{
			name:    "fenced imports are examples",
			engine:  "claude",
			content: "```\n@missing.md\n```\n",
			want:    20,
		},
		{
			name:    "missing import",
			engine:  "copilot",
			content: "@missing.md",
			failure: "read instruction file missing.md",
		},
		{
			name:    "import cycle",
			engine:  "antigravity",
			content: "@[rules](rules.md)",
			files:   map[string]string{"rules.md": "@[root](AGENTS.md)"},
			failure: "instruction import cycle",
		},
		{
			name:    "outside project",
			engine:  "copilot",
			content: "@../outside.md",
			failure: "must stay inside the project",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range tt.files {
				writeInstructionFixture(t, root, path, content)
			}
			cfg := &config.Config{Providers: map[string]config.Provider{tt.engine: {}}}

			checks, err := checkInstructions(t.Context(), root, cfg, []byte(tt.content))

			if len(tt.failure) > 0 {
				if err == nil || !strings.Contains(err.Error(), tt.failure) {
					t.Fatalf("check = %v, want %s", err, tt.failure)
				}
				return
			}
			if err != nil || len(checks) == 0 || checks[0].bytes != tt.want {
				t.Fatalf("checks = %v, %v; want %d bytes", checks, err, tt.want)
			}
		})
	}
}

func TestInstructionCheckUsesRenderedContent(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("x", 24001)
	const previous = "previous instructions"
	writeInstructionFixture(t, root, provider.AgentsFile, previous)
	cfg := &config.Config{}

	checks, err := checkInstructions(t.Context(), root, cfg, []byte(content))

	if err != nil || len(checks) != 1 || len(checks[0].warning) == 0 {
		t.Fatalf("checks = %v, %v", checks, err)
	}
	got, err := os.ReadFile(filepath.Join(root, provider.AgentsFile))
	if err != nil || string(got) != previous {
		t.Fatalf("check changed instructions: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := checkInstructions(ctx, root, cfg, []byte(content)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled check = %v", err)
	}

	if err := os.Remove(filepath.Join(root, provider.AgentsFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := checkInstructions(t.Context(), root, cfg, []byte(content)); err != nil {
		t.Fatalf("check required an existing generated file: %v", err)
	}
}

func TestLocalCodexInstructionLimit(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeInstructionFixture(t, home, "config.toml", "project_doc_max_bytes = 50000\n")
	writeInstructionFixture(t, root, ".codex/config.toml", "project_doc_max_bytes = 65536\n")
	cfg := &config.Config{Providers: map[string]config.Provider{"codex": {}}}

	checks, err := checkInstructions(t.Context(), root, cfg, []byte(strings.Repeat("x", 50001)))

	if err != nil || checks[0].limitBytes != 65536 || len(checks[0].warning) > 0 {
		t.Fatalf("checks = %v, %v", checks, err)
	}
	writeInstructionFixture(t, root, ".codex/config.toml", "project_doc_max_bytes = 0\n")
	checks, err = checkInstructions(t.Context(), root, cfg, []byte("instructions"))
	if err != nil || checks[0].limitBytes != 0 || len(checks[0].warning) == 0 {
		t.Fatalf("zero budget = %v, %v", checks, err)
	}
	for _, content := range []string{"project_doc_max_bytes = -1", `project_doc_max_bytes = "large"`} {
		writeInstructionFixture(t, root, ".codex/config.toml", content)
		if _, err := checkInstructions(t.Context(), root, cfg, []byte("instructions")); err == nil {
			t.Fatalf("invalid limit accepted: %s", content)
		}
	}
}

func writeInstructionFixture(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowInstructionEngines(t *testing.T) {
	root := t.TempDir()
	writeInstructionFixture(t, root, ".github/workflows/900-mcp.md", "---\ndescription: MCP only\n---\n")
	writeInstructionFixture(
		t,
		root,
		".github/workflows/910-codex.md",
		"---\non: issues\nengine:\n  id: codex\n  config: |\n    project_doc_max_bytes = 65536\n---\n",
	)
	writeInstructionFixture(
		t,
		root,
		".github/workflows/920-imported.md",
		"---\non: issues\nimports: [shared.md]\n---\n",
	)
	writeInstructionFixture(t, root, ".github/workflows/shared.md", "---\nengine: claude\n---\n")
	writeInstructionFixture(t, root, ".github/workflows/930-default.md", "---\non: issues\n---\n")
	writeInstructionFixture(t, root, ".github/workflows/940-antigravity.md", "---\non: issues\nengine: agy\n---\n")

	checks, err := checkInstructions(t.Context(), root, &config.Config{}, []byte(strings.Repeat("x", 40000)))

	if err != nil || len(checks) != 4 {
		t.Fatalf("checks = %v, %v", checks, err)
	}
	for i, want := range []struct {
		engine string
		limit  int
		warn   bool
	}{
		{
			engine: "codex",
			limit:  65536,
		},
		{
			engine: "claude",
			limit:  4194304,
		},
		{
			engine: "copilot",
			limit:  24000,
			warn:   true,
		},
		{
			engine: "antigravity",
			limit:  24000,
			warn:   true,
		},
	} {
		got := checks[i]
		if got.engine != want.engine || got.limitBytes != want.limit || (len(got.warning) > 0) != want.warn {
			t.Fatalf("check[%d] = %+v, want %+v", i, got, want)
		}
	}
}

func TestWorkflowInstructionCheckFailures(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		shared  string
		failure string
	}{
		{
			name:    "unknown engine",
			content: "on: issues\nengine: unknown\n",
			failure: "cannot determine instruction limit",
		},
		{
			name:    "invalid Codex limit",
			content: "on: issues\nengine:\n  id: codex\n  config: 'project_doc_max_bytes = -1'\n",
			failure: "must be a non-negative integer",
		},
		{
			name:    "missing import",
			content: "on: issues\nimports: [missing.md]\n",
			failure: "read workflow",
		},
		{
			name:    "import cycle",
			content: "on: issues\nimports: [shared.md]\n",
			shared:  "imports: [workflow.md]\n",
			failure: "workflow import cycle",
		},
		{
			name:    "remote import",
			content: "on: issues\nimports: [org/repo/rules.md@main]\n",
			failure: "cannot resolve workflow engine",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeInstructionFixture(t, root, ".github/workflows/workflow.md", "---\n"+tt.content+"---\n")
			if len(tt.shared) > 0 {
				writeInstructionFixture(t, root, ".github/workflows/shared.md", "---\n"+tt.shared+"---\n")
			}

			_, err := checkInstructions(t.Context(), root, &config.Config{}, []byte("instructions"))

			if err == nil || !strings.Contains(err.Error(), tt.failure) {
				t.Fatalf("check = %v, want %s", err, tt.failure)
			}
		})
	}
}
