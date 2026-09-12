package provider

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/provider/mocks"
)

type testProvider interface {
	Generate(map[string]any, config.Provider) error
	writeJSONFile(string, any) error
}

func makeProvider(
	t *testing.T,
	name string,
	cfg *config.Config,
	engine TemplateEngineAccessor,
	dir, out string,
) testProvider {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	switch name {
	case Claude:
		return NewClaudeProvider(logger, cfg, engine, dir, out)
	case Gemini:
		return NewGeminiProvider(logger, cfg, engine, dir, out)
	case Crush:
		return NewCrushProvider(logger, cfg, engine, dir, out)
	case Copilot:
		return NewCopilotProvider(logger, cfg, engine, dir, out)
	default:
		t.Fatalf("unknown provider %s", name)
		return nil
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProviderGenerationErrors(t *testing.T) {
	for _, name := range []string{Claude, Gemini, Crush, Copilot} {
		t.Run(name, func(t *testing.T) {
			for _, stage := range []string{"template", "chunks", "modes", "workflows", "process", "output", "settings"} {
				t.Run(stage, func(t *testing.T) {
					dir, out := t.TempDir(), t.TempDir()
					writeTestFile(t, filepath.Join(dir, "main.tpl.md"), "source")
					pc := config.Provider{Template: "main.tpl.md", Output: "instructions.md"}
					engine := mocks.NewMockTemplateEngineAccessor(gomock.NewController(t))
					processErr := errors.New("template execution failed")
					want := ""
					switch stage {
					case "template":
						pc.Template = "missing"
						want = "failed to load template"
					case "chunks":
						pc.Chunks = []string{"missing"}
						want = "failed to load chunks"
					case "modes":
						pc.Modes = []string{"missing"}
						want = "failed to load modes"
					case "workflows":
						pc.Workflows = []string{"missing"}
						want = "failed to load workflows"
					case "process":
						engine.EXPECT().Process("source", gomock.Any()).Return("", processErr)
						want = "failed to process template"
					case "output":
						if err := os.Mkdir(filepath.Join(out, pc.Output), 0755); err != nil {
							t.Fatal(err)
						}
						want = "failed to write output"
					case "settings":
						path := map[string]string{Claude: ".claude/settings.json", Gemini: ".gemini/settings.json", Crush: ".crush.json", Copilot: ".github/.copilot.mcp.json"}[name]
						if err := os.MkdirAll(filepath.Join(out, path), 0755); err != nil {
							t.Fatal(err)
						}
						want = "failed to generate config files"
					}
					if stage == "output" || stage == "settings" {
						engine.EXPECT().Process("source", gomock.Any()).Return("processed", nil)
					}
					p := makeProvider(t, name, &config.Config{}, engine, dir, out)
					err := p.Generate(map[string]any{}, pc)
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("Generate() = %v, want %q", err, want)
					}
					if stage == "process" && !errors.Is(err, processErr) {
						t.Error("template error identity lost")
					}
				})
			}
		})
	}
}

func TestProviderJSONWriting(t *testing.T) {
	for _, name := range []string{Claude, Gemini, Crush, Copilot} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := makeProvider(t, name, &config.Config{}, nil, dir, dir)
			path := filepath.Join(dir, "nested", "config.json")
			if err := p.writeJSONFile(path, map[string]any{"enabled": true, "name": "example"}); err != nil {
				t.Fatal(err)
			}
			if err := p.writeJSONFile(path, make(chan int)); err == nil {
				t.Fatal("unsupported JSON value was accepted")
			}
			if err := p.writeJSONFile(dir, map[string]string{}); err == nil {
				t.Fatal("JSON write error was swallowed")
			}
		})
	}
}

func TestBaseProviderFiles(t *testing.T) {
	dir := t.TempDir()
	p := NewBaseProvider(slog.New(slog.DiscardHandler), nil, nil, dir, dir)
	writeTestFile(t, filepath.Join(dir, "a.md"), "  alpha \n")
	writeTestFile(t, filepath.Join(dir, "b.md"), "\n beta ")
	contents, err := p.loadTemplates([]string{"a.md", "b.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.mergeStrings(contents); got != "alpha\n\nbeta" {
		t.Fatalf("merge = %q", got)
	}
	if got := p.mergeStrings(nil); got != "" {
		t.Fatalf("empty merge = %q", got)
	}
	if _, err := p.loadTemplates([]string{"a.md", "missing"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load error = %v", err)
	}
	if err := p.writeOutput(
		filepath.Join(dir, "a.md", "child"),
		"data",
	); err == nil ||
		!strings.Contains(err.Error(), "create output directory") {
		t.Fatalf("mkdir error = %v", err)
	}
	path := filepath.Join(dir, "new", "nested", "output.md")
	if err := p.writeOutput(path, "rendered"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "rendered" {
		t.Fatalf("written output = %q, %v", data, err)
	}
}

func TestClaudeAgents(t *testing.T) {
	for _, stage := range []string{"success", "directory", "read", "process", "write"} {
		t.Run(stage, func(t *testing.T) {
			dir, out := t.TempDir(), t.TempDir()
			writeTestFile(t, filepath.Join(dir, "agents", "reviewer.tpl.md"), "Review {{ .project }}")
			engine := mocks.NewMockTemplateEngineAccessor(gomock.NewController(t))
			p := NewClaudeProvider(slog.New(slog.DiscardHandler), &config.Config{}, engine, dir, out)
			pc := config.Provider{Agents: []string{"agents/reviewer.tpl.md"}}
			data := map[string]any{"project": "example"}
			want := ""
			switch stage {
			case "directory":
				writeTestFile(t, filepath.Join(out, ".claude"), "blocked")
				want = "create agents directory"
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
				want = "write agent"
			}
			if stage == "success" || stage == "write" {
				engine.EXPECT().Process("Review {{ .project }}", data).Return("Review example", nil)
			}
			err := p.copyAgents(pc, data)
			if want != "" {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("copyAgents() = %v, want %s", err, want)
				}
				return
			}
			if err != nil {
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
			writeTestFile(t, filepath.Join(dir, "main"), "source")
			engine := mocks.NewMockTemplateEngineAccessor(gomock.NewController(t))
			engine.EXPECT().Process("source", gomock.Any()).Return("rendered", nil)
			pc := config.Provider{Template: "main", Output: "CLAUDE.md"}
			want := ""
			if stage == "mcp" {
				if err := os.Mkdir(filepath.Join(out, ".mcp.json"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "failed to write"
			} else {
				pc.Agents = []string{"missing"}
				want = "failed to copy agents"
			}
			p := NewClaudeProvider(slog.New(slog.DiscardHandler), &config.Config{}, engine, dir, out)
			if err := p.Generate(nil, pc); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Generate() = %v, want %s", err, want)
			}
		})
	}
}

func TestMCPNamesAndSecretReferences(t *testing.T) {
	if got := MCPToolName("unknown", "example", "tool"); got != "tool" {
		t.Fatalf("unknown provider = %q", got)
	}
	for _, tt := range []struct{ input, want string }{
		{"literal", "literal"}, {"$TOKEN", "$COPILOT_MCP_TOKEN"}, {"${TOKEN}", "${COPILOT_MCP_TOKEN}"},
		{"${TOKEN:-default}", "${COPILOT_MCP_TOKEN:-default}"}, {"$COPILOT_MCP_TOKEN", "$COPILOT_MCP_TOKEN"},
		{"Bearer ${FIRST}:$SECOND", "Bearer ${COPILOT_MCP_FIRST}:$COPILOT_MCP_SECOND"},
		{"cost $5", "cost $5"},
	} {
		if got := copilotSecretReferences(tt.input); got != tt.want {
			t.Errorf("references(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	p := NewCopilotProvider(
		slog.New(slog.DiscardHandler),
		&config.Config{MCP: map[string]config.MCPServer{"empty": {Enabled: true, Command: "example"}}},
		nil,
		"",
		"",
	)
	if p.extractMCPServers()["empty"].Tools == nil {
		t.Error("Copilot requires a tools array, even when empty")
	}
}
