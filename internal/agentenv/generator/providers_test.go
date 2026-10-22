package generator

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
)

func TestGenerateGHAWBindings(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	t.Setenv("TOKEN", "must-not-appear-in-workflow")
	const base = `version: "1.0"
project: {name: example}
context: {template: main.tpl.md}
providers:
  gh-aw:
    bindings:
      TOKEN: '${{ secrets.SHARED_TOKEN }}'
mcp:
  remote:
    enabled: true
    name: remote
    type: http
    url: https://example.com/mcp
    headers:
      Authorization: 'Bearer ${TOKEN}'
`
	for _, tt := range []struct {
		name  string
		local string
		want  string
	}{
		{
			name: "shared",
			want: "Bearer ${{ secrets.SHARED_TOKEN }}",
		},
		{
			name:  "local override",
			local: "providers: {gh-aw: {bindings: {TOKEN: '${{ github.token }}'}}}",
			want:  "Bearer ${{ github.token }}",
		},
		{
			name:  "missing",
			local: "providers: {gh-aw: {bindings: null}}",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.ParseProjectConfig([]byte(base), []byte(tt.local), nil)
			if err != nil {
				t.Fatal(err)
			}
			dir, out := t.TempDir(), t.TempDir()
			if err := os.WriteFile(
				filepath.Join(dir, "main.tpl.md"),
				[]byte("# {{ .project.name }}"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			g := NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, out)

			err = g.Generate(t.Context(), false)

			if len(tt.want) == 0 {
				if err == nil || !strings.Contains(err.Error(), "providers.gh-aw.bindings.TOKEN") {
					t.Fatalf("Generate() = %v, want missing binding error", err)
				}
				for _, path := range []string{provider.AgentsFile, ghaw.MCPFile} {
					if _, err := os.Stat(filepath.Join(out, path)); !os.IsNotExist(err) {
						t.Fatalf("generation changed %s despite missing binding: %v", path, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(out, ghaw.MCPFile))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), tt.want) ||
				strings.Contains(string(content), "must-not-appear-in-workflow") {
				t.Fatalf("generated workflow did not use the configured binding: %s", content)
			}
		})
	}
}

func TestGenerateProviderToggles(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	settingsFiles := map[string]string{
		claude.Name:      ".claude/settings.local.json",
		codex.Name:       ".codex/config.toml",
		antigravity.Name: ".agents/plugins/project-tools/mcp_config.json",
		crush.Name:       ".crush.json",
		copilot.Name:     ".mcp.json",
		ghaw.Name:        ghaw.MCPFile,
	}
	for name := range settingsFiles {
		t.Run(name, func(t *testing.T) {
			enabled, disabled := true, false
			cfg := &config.Config{
				Version:   "1.0",
				Project:   config.Project{Name: "example"},
				Context:   config.Context{Template: "main.tpl.md"},
				Providers: make(map[string]config.Provider),
				MCP: map[string]provider.MCPServer{
					"example": {
						Enabled: true,
						Name:    "example",
						Command: "example",
					},
				},
			}
			for provider := range settingsFiles {
				cfg.Providers[provider] = config.Provider{Enabled: &disabled}
			}
			cfg.Providers[name] = config.Provider{Enabled: &enabled}

			if name == ghaw.Name {
				server := cfg.MCP["example"]
				server.Command = "npx"
				cfg.MCP["example"] = server
			}
			if name != claude.Name && name != crush.Name && name != ghaw.Name {
				server := cfg.MCP["example"]
				server.CWD = "src"
				cfg.MCP["example"] = server
			}
			dir, out := t.TempDir(), t.TempDir()
			if err := os.WriteFile(
				filepath.Join(dir, "main.tpl.md"),
				[]byte("# {{ .project.name }}"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			g := NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, out)
			if err := g.Generate(t.Context(), false); err != nil {
				t.Fatal(err)
			}
			want := []string{"AGENTS.md", settingsFiles[name]}

			if name == claude.Name {
				want = append(want, ".mcp.json")
			}
			if name == antigravity.Name {
				want = append(want, ".agents/plugins/project-tools/plugin.json")
				settings := readJSON[map[string]any](t, out, settingsFiles[name])
				server := settings["mcpServers"].(map[string]any)["example"].(map[string]any)

				if server["cwd"] != filepath.Join(out, "src") {
					t.Errorf("Antigravity cwd = %v, want project-relative absolute path", server["cwd"])
				}
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

			// Disabling every provider preserves settings and instructions, including with --clean.
			preserved := make(map[string]string)
			for _, path := range files {
				data, err := os.ReadFile(filepath.Join(out, path))
				if err != nil {
					t.Fatal(err)
				}
				preserved[path] = string(data)
			}
			cfg.Providers[name] = config.Provider{Enabled: &disabled}
			cfg.Providers[claude.Name] = config.Provider{
				Enabled: &disabled,
				Agents:  []string{"missing.tpl.md"},
			}
			cfg.MCP["example"] = provider.MCPServer{
				Enabled: true,
				Name:    "example",
				Command: "changed",
			}
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
			}
		})
	}
}
