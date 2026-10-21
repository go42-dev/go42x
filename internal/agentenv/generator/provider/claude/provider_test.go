package claude

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/mocks"
	"github.com/go42-dev/go42x/internal/fspatch"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeAgents(t *testing.T) {
	for _, stage := range []string{"success", "directory", "read", "process", "write"} {
		t.Run(stage, func(t *testing.T) {
			dir, out := t.TempDir(), t.TempDir()
			writeTestFile(t, filepath.Join(dir, "agents", "reviewer.tpl.md"), "Review {{ .project }}")
			engine := mocks.NewMockTemplateEngineAccessor(gomock.NewController(t))
			plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), out, filepath.Join(out, ".go42x", "backups"))
			pc := Settings{
				OutputDir: out,
				Agents:    []string{"agents/reviewer.tpl.md"},
			}
			data := map[string]any{"project": "example"}
			want := ""
			switch stage {
			case "directory":
				writeTestFile(t, filepath.Join(out, ".claude"), "blocked")
				want = "prepare agent"
			case "read":
				pc.Agents = []string{"missing"}
				want = "read agent template"
			case "process":
				engine.EXPECT().Process("Review {{ .project }}", data).Return("", errors.New("bad template"))
				want = "process agent template"
			case "write":
				if err := os.MkdirAll(filepath.Join(out, ".claude/agents/reviewer.md"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "prepare agent"
			}
			if stage == "success" || stage == "write" || stage == "directory" {
				engine.EXPECT().Process("Review {{ .project }}", data).Return("Review example", nil)
			}
			p := New(slog.New(slog.DiscardHandler), engine, pc, dir)
			err := p.prepareAgents(plan, data)

			if len(want) > 0 {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("prepareAgents() = %v, want %s", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			result, err := os.ReadFile(filepath.Join(out, ".claude/agents/reviewer.md"))
			if err != nil || string(result) != "Review example" {
				t.Fatalf("agent output = %q, %v", result, err)
			}
		})
	}
}

func TestClaudeAdditionalOutputFailures(t *testing.T) {
	for _, stage := range []string{"mcp", "agents"} {
		t.Run(stage, func(t *testing.T) {
			dir, out := t.TempDir(), t.TempDir()
			engine := mocks.NewMockTemplateEngineAccessor(gomock.NewController(t))
			pc := Settings{
				OutputDir:    out,
				WriteMCPFile: true,
			}
			want := ""

			if stage == "mcp" {
				if err := os.Mkdir(filepath.Join(out, ".mcp.json"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "failed to prepare"
			} else {
				pc.Agents = []string{"missing"}
				want = "failed to prepare agents"
			}

			plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), out, filepath.Join(out, ".go42x", "backups"))
			p := New(slog.New(slog.DiscardHandler), engine, pc, dir)
			if err := p.Prepare(plan, nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Prepare() = %v, want %s", err, want)
			}
		})
	}
}
