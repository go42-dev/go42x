package ghaw

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/fspatch"
)

func TestGHAWMCPFragment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "local-token-must-not-be-written")
	t.Setenv("API_KEY", "local-key-must-not-be-written")
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".github/workflows/910-aw-description.md")
	workflow := "---\non: pull_request\nengine: codex\nimports: [900-aw-mcp.md]\n---\nDescribe the PR.\n"
	writeTestFile(t, workflowPath, workflow)
	cfg := Settings{OutputDir: root, MCPServers: map[string]provider.MCPServer{
		"local": {
			Enabled: true,
			Name:    "local",
			Command: "npx",
			Args:    []string{"tool", "--", "gopls", "mcp"},
			Env: map[string]string{
				"API_KEY": "$API_KEY",
				"LABEL":   "literal",
			},
			Tools: []string{"go_diagnostics"},
		},
		"remote": {
			Enabled: true,
			Name:    "remote",
			Type:    provider.MCPServerTypeHTTP,
			URL:     "https://api.example.com/mcp?key=${API_KEY}",
			Headers: map[string]string{
				"Authorization": "Bearer ${GITHUB_TOKEN}",
				"X-API-Key":     "$API_KEY",
				"X-Label":       "${{ vars.LABEL }}",
			},
		},
		"disabled.invalid": {
			Name: "disabled",
			Type: provider.MCPServerTypeSSE,
			CWD:  "unused",
		},
	}}
	before, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	p := New(logger, cfg, nil, "")
	plan := fspatch.NewPlan(logger, root, filepath.Join(root, ".go42x/backups"))

	if err := p.Prepare(plan, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, MCPFile)); !os.IsNotExist(err) {
		t.Fatalf("Prepare wrote its output: %v", err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, MCPFile))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.HasPrefix(content, []byte(ghAWFrontmatter)) || !bytes.HasSuffix(content, []byte(ghAWFrontmatter)) {
		t.Fatalf("fragment is missing Markdown front matter: %s", content)
	}
	var fragment MCPConfig
	if err := yaml.Unmarshal(content[len(ghAWFrontmatter):len(content)-len(ghAWFrontmatter)], &fragment); err != nil {
		t.Fatal(err)
	}
	want := map[string]MCPServer{
		"local": {
			Type:    provider.MCPServerTypeStdio,
			Command: "npx",
			Args:    []string{"tool", "--", "gopls", "mcp"},
			Env: map[string]string{
				"API_KEY": "${{ secrets.API_KEY }}",
				"LABEL":   "literal",
			},
			Allowed: []string{"*"},
		},
		"remote": {
			Type: provider.MCPServerTypeHTTP,
			URL:  "https://api.example.com/mcp?key=${{ secrets.API_KEY }}",
			Headers: map[string]string{
				"Authorization": "Bearer ${{ github.token }}",
				"X-API-Key":     "${{ secrets.API_KEY }}",
				"X-Label":       "${{ vars.LABEL }}",
			},
			Allowed: []string{"*"},
		},
	}
	if fragment.Description != ghAWMCPDescription || !reflect.DeepEqual(fragment.MCPServers, want) {
		t.Fatalf("fragment = %+v; want servers %+v", fragment, want)
	}
	if strings.Contains(string(content), "local-token-must-not-be-written") ||
		strings.Contains(string(content), "local-key-must-not-be-written") ||
		strings.Contains(string(content), "\non:") || strings.Contains(string(content), "\nengine:") {
		t.Fatalf("fragment contains local credentials or workflow configuration: %s", content)
	}
	if got, err := os.ReadFile(workflowPath); err != nil || string(got) != workflow {
		t.Fatalf("authored workflow changed: %q, %v", got, err)
	}
	after, err := yaml.Marshal(cfg)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("provider changed shared configuration: %v", err)
	}
	plan = fspatch.NewPlan(logger, root, filepath.Join(root, ".go42x/backups"))
	if err := p.Prepare(plan, nil); err != nil || len(plan.Changes()) > 0 {
		t.Fatalf("generation is not deterministic: changes %v, error %v", plan.Changes(), err)
	}

	cfg.MCPServers = nil
	p = New(logger, cfg, nil, "")
	plan = fspatch.NewPlan(logger, root, filepath.Join(root, ".go42x/backups"))
	if err := p.Prepare(plan, nil); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(filepath.Join(root, MCPFile))
	if err != nil || !strings.Contains(string(content), "mcp-servers: {}") {
		t.Fatalf("stale servers were retained: %s, %v", content, err)
	}
}

func TestGHAWUnsupportedMCPSettings(t *testing.T) {
	for _, tt := range []struct {
		name   string
		server provider.MCPServer
		want   string
	}{
		{"SSE", provider.MCPServer{Type: provider.MCPServerTypeSSE}, "only stdio and HTTP"},
		{"runner command", provider.MCPServer{Command: "task"}, "cannot run this stdio command directly"},
		{"cwd", provider.MCPServer{
			Command: "task",
			CWD:     "src",
		}, "does not support cwd"},
		{"HTTP env", provider.MCPServer{
			Type: provider.MCPServerTypeHTTP,
			Env:  map[string]string{"KEY": "value"},
		}, "does not support env"},
		{"environment name", provider.MCPServer{
			Command: "npx",
			Env:     map[string]string{"bad-name": "value"},
		}, "environment variable names"},
		{"header name", provider.MCPServer{
			Type:    provider.MCPServerTypeHTTP,
			Headers: map[string]string{"Bad Header": "value"},
		}, "HTTP header names"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ghAWMCPServer(tt.server); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ghAWMCPServer() = %v, want %s", err, tt.want)
			}
		})
	}
	root := t.TempDir()
	logger := slog.New(slog.DiscardHandler)
	for _, name := range []string{"invalid.name", ""} {
		p := New(logger, Settings{OutputDir: root, MCPServers: map[string]provider.MCPServer{
			name: {Enabled: true, Command: "task"},
		}}, nil, "")
		plan := fspatch.NewPlan(logger, root, filepath.Join(root, ".go42x/backups"))
		if err := p.Prepare(
			plan,
			nil,
		); err == nil ||
			!strings.Contains(err.Error(), "gh-aw requires names") {
			t.Fatalf("invalid server name: %v", err)
		}
		if len(plan.Changes()) > 0 {
			t.Fatal("failed preparation scheduled output changes")
		}
	}
}

func TestGHAWEnvironmentExpressions(t *testing.T) {
	for _, tt := range []struct{ value, want string }{
		{"Bearer $GITHUB_TOKEN", "Bearer ${{ github.token }}"},
		{"${GITHUB_TOKEN}", "${{ github.token }}"},
		{"$API_KEY/${SECOND_KEY}", "${{ secrets.API_KEY }}/${{ secrets.SECOND_KEY }}"},
		{"${{ secrets.API_KEY }}", "${{ secrets.API_KEY }}"},
		{"${{ vars.MODEL }}", "${{ vars.MODEL }}"},
		{"${{ github.token }}", "${{ github.token }}"},
		{"literal $5 ${} end", "literal $5 ${} end"},
	} {
		if got := ghAWExpression(tt.value); got != tt.want {
			t.Errorf("ghAWExpression(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
	entry, err := ghAWMCPServer(provider.MCPServer{
		Command: "npx",
		Args:    []string{"--token", "$API_KEY"},
	})
	if err != nil || !slices.Equal(entry.Args, []string{"--token", "${{ secrets.API_KEY }}"}) {
		t.Fatalf("argument references = %v, %v", entry.Args, err)
	}
}

func TestGHAWContainerPreservesSharedCommand(t *testing.T) {
	root := t.TempDir()
	server := provider.MCPServer{
		Enabled: true,
		Name:    "local",
		Command: "task",
		Args:    []string{"x", "--", "mcp"},
		Env:     map[string]string{"KEY": "$KEY", "SHARED": "$SHARED"},
		Container: &provider.MCPContainer{
			Image:   "ghcr.io/example/tools",
			Version: "${{ env.GO42X_MCP_IMAGE_VERSION }}",
			Command: "go42x",
			Args:    []string{"mcp"},
			WorkDir: "${{ github.workspace }}",
			Mounts:  []string{"${{ github.workspace }}:${{ github.workspace }}:rw"},
			Env: map[string]string{
				"KEY":                       "$CONTAINER_KEY",
				"MISE_TRUSTED_CONFIG_PATHS": "${{ github.workspace }}/etc/mise.toml",
			},
		},
	}
	cfg := Settings{
		OutputDir:  root,
		MCPServers: map[string]provider.MCPServer{"local": server},
	}
	logger := slog.New(slog.DiscardHandler)
	plan := fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))
	p := New(logger, cfg, nil, "")
	if err := p.Prepare(plan, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.MCPServers["local"], server) || cfg.MCPServers["local"].Env["KEY"] != "$KEY" {
		t.Fatal("shared stdio configuration was modified")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, MCPFile))
	if err != nil {
		t.Fatal(err)
	}
	var fragment MCPConfig
	if err := yaml.Unmarshal(bytes.Split(data, []byte(ghAWFrontmatter))[1], &fragment); err != nil {
		t.Fatal(err)
	}
	entry := fragment.MCPServers["local"]
	if entry.Type != provider.MCPServerTypeStdio || entry.Container != "ghcr.io/example/tools" ||
		entry.Version != "${{ env.GO42X_MCP_IMAGE_VERSION }}" ||
		entry.Entrypoint != "go42x" || entry.Command != "" ||
		!slices.Equal(entry.EntrypointArgs, []string{"mcp"}) ||
		!slices.Equal(entry.Args, []string{"--workdir", "${{ github.workspace }}"}) ||
		!slices.Equal(entry.Mounts, []string{"${{ github.workspace }}:${{ github.workspace }}:rw"}) ||
		entry.Env["KEY"] != "${{ secrets.CONTAINER_KEY }}" ||
		entry.Env["SHARED"] != "${{ secrets.SHARED }}" ||
		entry.Env["MISE_TRUSTED_CONFIG_PATHS"] != "${{ github.workspace }}/etc/mise.toml" {
		t.Fatalf("container declaration lost command, environment, or workspace: %+v", entry)
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
