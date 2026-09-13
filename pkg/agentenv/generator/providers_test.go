package generator

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

func TestGenerateProviderToggles(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	settingsFiles := map[string]string{
		"claude":  ".claude/settings.local.json",
		"codex":   ".codex/config.toml",
		"gemini":  ".gemini/settings.json",
		"crush":   ".crush.json",
		"copilot": ".mcp.json",
	}
	for _, name := range []string{"claude", "codex", "gemini", "crush", "copilot"} {
		t.Run(name, func(t *testing.T) {
			enabled, disabled := true, false
			cfg := &config.Config{
				Version:   "1.0",
				Project:   config.Project{Name: "example"},
				Context:   config.Context{Template: "main.tpl.md"},
				Providers: make(map[string]config.Provider),
				MCP: map[string]config.MCPServer{
					"example": {Enabled: true, Name: "example", Command: "example"},
				},
			}
			for provider := range settingsFiles {
				cfg.Providers[provider] = config.Provider{Enabled: &disabled}
			}
			cfg.Providers[name] = config.Provider{Enabled: &enabled}
			if name != "claude" && name != "crush" {
				server := cfg.MCP["example"]
				server.CWD = "src"
				cfg.MCP["example"] = server
			}
			dir, out := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.tpl.md"), []byte("{{ .project.name }}"), 0600); err != nil {
				t.Fatal(err)
			}
			g := NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, out)
			if err := g.Generate(t.Context(), false); err != nil {
				t.Fatal(err)
			}
			want := []string{"AGENTS.md", settingsFiles[name]}
			if name == "claude" {
				want = append(want, "CLAUDE.md", ".mcp.json")
			}
			if name == "gemini" {
				want = append(want, "GEMINI.md")
			}
			var files []string
			if err := filepath.WalkDir(out, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				rel, err := filepath.Rel(out, path)
				files = append(files, filepath.ToSlash(rel))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			slices.Sort(files)
			slices.Sort(want)
			if !slices.Equal(files, want) {
				t.Fatalf("generated files = %v, want %v", files, want)
			}

			// Disabling every provider preserves settings, including with --clean.
			preserved := make(map[string]string)
			for _, path := range files {
				if path == "CLAUDE.md" || path == "GEMINI.md" {
					continue
				}
				data, err := os.ReadFile(filepath.Join(out, path))
				if err != nil {
					t.Fatal(err)
				}
				preserved[path] = string(data)
			}
			cfg.Providers[name] = config.Provider{Enabled: &disabled}
			cfg.Providers["claude"] = config.Provider{Enabled: &disabled, Agents: []string{"missing.tpl.md"}}
			cfg.MCP["example"] = config.MCPServer{Enabled: true, Name: "example", Command: "changed"}
			for _, clean := range []bool{false, true} {
				g = NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, out)
				if err := g.Generate(t.Context(), clean); err != nil {
					t.Fatal(err)
				}
				for path, want := range preserved {
					data, err := os.ReadFile(filepath.Join(out, path))
					if err != nil || string(data) != want {
						t.Errorf("preserved %s = %q, %v; want %q", path, data, err, want)
					}
				}
				for _, path := range []string{"CLAUDE.md", "GEMINI.md"} {
					if _, err := os.Stat(filepath.Join(out, path)); !os.IsNotExist(err) {
						t.Errorf("disabled provider import %s still exists: %v", path, err)
					}
				}
			}
		})
	}
}
