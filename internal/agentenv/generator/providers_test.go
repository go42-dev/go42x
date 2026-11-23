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
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
)

func TestGenerateGHAWServers(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	const base = `version: "1.0"
project: {name: example}
context: {template: main.tpl.md}
providers:
  claude: {}
  gh-aw:
    mcp-servers: [workflow]
mcp:
  local:
    enabled: true
    name: local
    command: local-server
  workflow:
    enabled: false
    name: workflow
    command: npx
    args: [workflow-server]
`
	for _, tt := range []struct {
		providers    []string
		instructions string
		workflow     bool
	}{
		// gh-aw gets its listed servers, and instructions generated for it describe them.
		{providers: []string{ghaw.Name}, instructions: "local workflow ", workflow: true},
		// Local agents keep the enabled servers.
		{providers: []string{claude.Name}, instructions: "local "},
	} {
		t.Run(strings.Join(tt.providers, ","), func(t *testing.T) {
			cfg, err := config.ParseProjectConfig([]byte(base), nil, tt.providers, nil)
			if err != nil {
				t.Fatal(err)
			}
			dir, out := t.TempDir(), t.TempDir()
			template := "# Example\n\nServers: {{ range $name, $_ := .mcp }}{{ $name }} {{ end }}\n"
			if err := os.WriteFile(filepath.Join(dir, "main.tpl.md"), []byte(template), 0600); err != nil {
				t.Fatal(err)
			}

			if err := NewGenerator(
				slog.New(slog.DiscardHandler),
				cfg,
				dir,
				out,
			).Generate(t.Context(), false); err != nil {
				t.Fatal(err)
			}

			instructions, err := os.ReadFile(filepath.Join(out, provider.AgentsFile))
			if err != nil || !strings.Contains(string(instructions), "Servers: "+tt.instructions+"\n") {
				t.Errorf("instructions = %q, %v; want servers %q", instructions, err, tt.instructions)
			}
			workflow, err := os.ReadFile(filepath.Join(out, ghaw.MCPFile))
			if !tt.workflow {
				if !os.IsNotExist(err) {
					t.Errorf("gh-aw output exists without gh-aw: %v", err)
				}
				return
			}
			if err != nil || !strings.Contains(string(workflow), "mcp-workflow:") ||
				strings.Contains(string(workflow), "mcp-local:") {
				t.Errorf("gh-aw servers = %s, %v; want only the listed server", workflow, err)
			}
		})
	}
}

func TestGenerateGHAWBindings(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	t.Setenv("TOKEN", "must-not-appear-in-workflow")
	const base = `version: "1.0"
project: {name: example}
context: {template: main.tpl.md}
providers:
  gh-aw:
    mcp-servers: [remote]
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
			cfg, err := config.ParseProjectConfig([]byte(base), []byte(tt.local), nil, nil)
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
			if name == ghaw.Name {
				want = append(want, ghaw.JournalFile)
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
