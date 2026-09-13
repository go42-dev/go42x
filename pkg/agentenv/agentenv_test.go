package agentenv

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

func testService(t *testing.T, dir string, clean bool) *Service {
	t.Helper()
	t.Chdir(dir)
	s, err := NewAgentEnvService(&Settings{Clean: clean})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestServiceSettingsAndLogger(t *testing.T) {
	if _, err := NewAgentEnvService(nil); err == nil {
		t.Fatal("nil settings accepted")
	}
	logger := slog.New(slog.DiscardHandler)
	s, err := NewAgentEnvService(&Settings{}, WithLogger(logger))
	if err != nil || s.logger != logger {
		t.Fatalf("custom logger = %v, %v", s, err)
	}
	s, err = NewAgentEnvService(&Settings{}, WithLogger(nil))
	if err != nil || s.logger == nil {
		t.Fatalf("default logger = %v, %v", s, err)
	}
}

func TestInitExtractsTemplatesAndPreservesCustomFiles(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, ".go42x/chunks/10-personality.tpl.md")
	writeFile(t, custom, "custom personality")
	writeFile(t, filepath.Join(dir, ".gitignore"), "existing-entry\n")
	s := testService(t, dir, false)
	if err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, custom)) != "custom personality" {
		t.Fatal("custom template overwritten")
	}
	if _, err := config.LoadConfig(filepath.Join(dir, ".go42x/go42x.yaml")); err != nil {
		t.Fatal(err)
	}
	err := fs.WalkDir(templateFS, "template", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel("template", path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, ".go42x", relative)
		if target == custom {
			return nil
		}
		want, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(readFile(t, target), want) {
			t.Errorf("embedded template differs: %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(dir, ".gitignore"))
	if !bytes.HasPrefix(before, []byte("existing-entry\n")) {
		t.Fatal("existing gitignore changed")
	}
	for _, entry := range ignoreFiles {
		if strings.Count(string(before), "\n"+entry+"\n") != 1 {
			t.Errorf("ignore entry missing or duplicated: %s", entry)
		}
	}
	writeFile(t, filepath.Join(dir, ".go42x/go42x.yaml"), "custom configuration")
	if err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(dir, ".go42x/go42x.yaml"))) != "custom configuration" {
		t.Fatal("existing config overwritten")
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(dir, ".gitignore"))) {
		t.Fatal("repeated init changed gitignore")
	}
}

func TestInitFilesystemErrors(t *testing.T) {
	for _, stage := range []string{"configuration", "templates", "gitignore"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			want := ""
			switch stage {
			case "configuration":
				writeFile(t, filepath.Join(dir, ".go42x"), "blocked")
				want = "check configuration"
			case "templates":
				writeFile(t, filepath.Join(dir, ".go42x/chunks"), "blocked")
				want = "extract template"
			case "gitignore":
				if err := os.Mkdir(filepath.Join(dir, ".gitignore"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "update .gitignore"
			}
			if err := testService(t, dir, false).Init(t.Context()); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Init() = %v, want %s", err, want)
			}
		})
	}
}

func TestGitignoreIdempotence(t *testing.T) {
	dir := t.TempDir()
	if err := updateGitIgnore(dir); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, filepath.Join(dir, ".gitignore"))
	if err := updateGitIgnore(dir); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, readFile(t, filepath.Join(dir, ".gitignore"))) {
		t.Fatal("ignore rules duplicated")
	}
}

func TestGenerateAndCleanPreserveSources(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	dir := t.TempDir()
	s := testService(t, dir, true)
	if err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ".go42x/go42x.yaml")
	before := readFile(t, configPath)
	custom := filepath.Join(dir, ".go42x/custom.md")
	writeFile(t, custom, "preserve this")
	asset := filepath.Join(dir, ".claude/agents/custom.md")
	writeFile(t, asset, "preserve agent")
	if err := s.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if len(readFile(t, filepath.Join(dir, path))) == 0 {
			t.Errorf("empty output %s", path)
		}
		writeFile(t, filepath.Join(dir, path), "stale")
	}
	if err := s.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if string(readFile(t, filepath.Join(dir, path))) == "stale" {
			t.Errorf("stale output %s", path)
		}
	}
	if !bytes.Equal(before, readFile(t, configPath)) || string(readFile(t, custom)) != "preserve this" ||
		string(readFile(t, asset)) != "preserve agent" {
		t.Fatal("clean removed or changed user sources")
	}
	for _, path := range []string{".claude/settings.local.json", ".mcp.json", ".gemini/settings.json", ".crush.json"} {
		if len(readFile(t, filepath.Join(dir, path))) == 0 {
			t.Errorf("missing generated settings %s", path)
		}
	}
	// Optional project metadata must also work with the shipped templates.
	cfg.Project = config.Project{Name: "minimal"}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Generate(t.Context()); err != nil {
		t.Fatalf("minimal project generation: %v", err)
	}
}

func TestGenerateErrors(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	dir := t.TempDir()
	s := testService(t, dir, false)
	if err := s.Generate(t.Context()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing config error = %v", err)
	}
	if err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, output, template, want string
		clean                        bool
	}{
		{"missing template", "AGENTS.md", "missing", "generation failed", false},
		{"clean blocked", "AGENTS.md", "agents.tpl.md", "read instructions AGENTS.md", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Version:   "1.0",
				Project:   config.Project{Name: "test"},
				Context:   config.Context{Template: tt.template},
				Providers: map[string]config.Provider{"claude": {}},
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".go42x/go42x.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if tt.clean {
				writeFile(t, filepath.Join(dir, tt.output, "child"), "do not remove")
			}
			err = testService(t, dir, tt.clean).Generate(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Generate() = %v, want %s", err, tt.want)
			}
		})
	}
	writeFile(t, filepath.Join(dir, ".go42x/go42x.yaml"), "invalid: [")
	if err := s.Generate(t.Context()); err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("malformed config = %v", err)
	}
}
