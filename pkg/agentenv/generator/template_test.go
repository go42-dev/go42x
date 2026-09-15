package generator

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

func TestTemplateProcessing(t *testing.T) {
	e := newTemplateEngine(t.TempDir())
	for _, tt := range []struct{ name, template, want string }{
		{"context", "Hello {{ .name }}", "Hello World"},
		{"functions", `{{ lower .name }} {{ upper "go" }} {{ trim " x " }} {{ join .items "," }}`, "world GO x a,b"},
		{"MCP Claude", `{{ mcpTool "claude" "go42x" "kwb_search" }}`, "mcp__go42x__kwb_search"},
		{"MCP Gemini", `{{ mcpTool "gemini" "go42x" "kwb_search" }}`, "mcp_go42x_kwb_search"},
		{"MCP Crush", `{{ mcpTool "crush" "go42x" "kwb_search" }}`, "mcp_go42x_kwb_search"},
		{"MCP Copilot", `{{ mcpTool "copilot" "go42x" "kwb_search" }}`, "go42x/kwb_search"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Process(tt.template, map[string]any{"name": "World", "items": []string{"a", "b"}})
			if err != nil || got != tt.want {
				t.Fatalf("Process() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, tt := range []struct{ template, want string }{{"{{", "failed to parse template"}, {"{{ missingFunction }}", "failed to parse template"}, {"{{ upper .items }}", "failed to execute template"}} {
		if _, err := e.Process(
			tt.template,
			map[string]any{"items": []string{"a"}},
		); err == nil ||
			!strings.Contains(err.Error(), tt.want) {
			t.Fatalf("Process(%q) = %v, want %s", tt.template, err, tt.want)
		}
	}
}

func TestTemplateInjection(t *testing.T) {
	e := newTemplateEngine("")
	content := "before {{ .chunks }} after"
	content = e.InjectChunks(content, "chunk")
	if content != "before chunk after" {
		t.Fatalf("injected = %q", content)
	}
	if got := e.InjectChunks("no placeholder", "chunk"); got != "no placeholder" {
		t.Fatalf("unexpected injection: %q", got)
	}
	if got := e.InjectChunks("{{ .chunks }}", ""); got != "" {
		t.Fatalf("empty injection: %q", got)
	}
}

func TestContextMapsAreIndependent(t *testing.T) {
	ctx := newContext(t.Context())
	ctx.Set("name", "first")
	a := ctx.ToMap()
	a["name"] = "changed"
	a["provider"] = "claude"
	b := ctx.ToMap()
	if b["name"] != "first" || b["provider"] != nil {
		t.Fatalf("provider mutated shared context: %v", b)
	}
	ctx.Set("name", "second")
	if ctx.ToMap()["name"] != "second" || a["name"] != "changed" {
		t.Fatal("context replacement leaked across snapshots")
	}
}

// File-loading coverage relocated from BaseProvider with the shared renderer.
func TestTemplateFiles(t *testing.T) {
	dir := t.TempDir()
	e := newTemplateEngine(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.tpl.md"), []byte("  alpha \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.tpl.md"), []byte("\n beta "), 0600); err != nil {
		t.Fatal(err)
	}
	contents, err := e.loadTemplates(".")
	if err != nil {
		t.Fatal(err)
	}
	if got := mergeStrings(contents); got != "alpha\n\nbeta" {
		t.Fatalf("merge = %q", got)
	}
	if got := mergeStrings(nil); got != "" {
		t.Fatalf("empty merge = %q", got)
	}
	if _, err := e.loadTemplates("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load error = %v", err)
	}
}

// Rendering failures used to be exercised separately by every provider.
func TestTemplateGenerationErrors(t *testing.T) {
	for _, stage := range []string{"template", "chunks", "process", "output"} {
		t.Run(stage, func(t *testing.T) {
			dir, out := t.TempDir(), t.TempDir()
			content := "{{ .chunks }}"
			cfg := config.Context{Template: "main.tpl.md"}
			want := ""
			switch stage {
			case "template":
				cfg.Template = "missing"
				want = "failed to load template"
			case "chunks":
				cfg.ChunksDir = "missing"
				want = "failed to load chunks"
			case "process":
				content = "{{ upper .items }}"
				want = "failed to execute template"
			case "output":
				if err := os.Mkdir(filepath.Join(out, "AGENTS.md"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "prepare instructions AGENTS.md"
			}
			if err := os.WriteFile(filepath.Join(dir, "main.tpl.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			g := NewGenerator(slog.New(slog.DiscardHandler), &config.Config{Context: cfg}, dir, out)
			if err := g.prepareInstructions(
				output.NewPlan(slog.New(slog.DiscardHandler), out),
				map[string]any{"items": []string{"a"}},
				false,
			); err == nil ||
				!strings.Contains(err.Error(), want) {
				t.Fatalf("prepareInstructions() = %v, want %q", err, want)
			}
		})
	}
}
