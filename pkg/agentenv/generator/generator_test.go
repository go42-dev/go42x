package generator

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/mocks"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/provider"
)

func TestGeneratorContextAndProviderErrors(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	t.Setenv("AGENTENV_TEST_CONTEXT", "included")
	cfg := &config.Config{
		Version:   "1.0",
		Project:   config.Project{Name: "example"},
		EnvVars:   []string{"AGENTENV_TEST_CONTEXT"},
		Context:   config.Context{Template: "main.tpl.md"},
		Providers: map[string]config.Provider{"claude": {}, "gemini": {}},
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tpl.md"), []byte("{{ .project.name }}"), 0600); err != nil {
		t.Fatal(err)
	}
	g := NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, t.TempDir())
	ctrl := gomock.NewController(t)
	failures := []error{errors.New("claude failed"), errors.New("gemini failed")}
	for i, name := range []string{"claude", "gemini"} {
		p := mocks.NewMockproviderAccessor(ctrl)
		p.EXPECT().
			Prepare(gomock.Any(), gomock.Any(), cfg.Providers[name]).
			DoAndReturn(func(_ *output.Plan, data map[string]any, _ config.Provider) error {
				if data["provider"] != name {
					t.Errorf("provider context = %v, want %s", data["provider"], name)
				}
				if data["project"].(map[string]any)["name"] != "example" {
					t.Error("project context missing")
				}
				if data["environment"].(map[string]any)["variables"].(map[string]string)["AGENTENV_TEST_CONTEXT"] != "included" {
					t.Error("environment context missing")
				}
				for _, removed := range []string{"analysis", "conventions", "github_actions", "git", "mutated"} {
					if _, ok := data[removed]; ok {
						t.Errorf("unexpected context %s", removed)
					}
				}
				data["mutated"] = true
				return failures[i]
			})
		g.providers[name] = p
	}
	err := g.Generate(t.Context(), false)
	for _, failure := range failures {
		if !errors.Is(err, failure) {
			t.Errorf("Generate() = %v, missing wrapped %v", err, failure)
		}
	}
}

func TestGeneratorPreservesOutputsAfterProviderFailure(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	dir := t.TempDir()
	out := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "valid.tpl.md"),
		[]byte("{{ .project.name }}"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version: "1.0",
		Project: config.Project{Name: "example"},
		Context: config.Context{Template: "valid.tpl.md"},
		Providers: map[string]config.Provider{
			"claude": {}, "gemini": {},
		},
	}
	if err := os.MkdirAll(filepath.Join(out, ".claude/settings.local.json"), 0755); err != nil {
		t.Fatal(err)
	}
	g := NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, out)
	if err := g.Generate(t.Context(), false); err == nil || !strings.Contains(err.Error(), "provider claude") {
		t.Fatalf("Generate() = %v", err)
	}
	for _, path := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", ".gemini/settings.json"} {
		if _, err := os.Stat(filepath.Join(out, path)); !os.IsNotExist(err) {
			t.Fatalf("output %s changed despite preparation failure: %v", path, err)
		}
	}
}

func readJSON[T any](t *testing.T, root, path string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("invalid JSON in %s: %v", path, err)
	}
	return value
}

func TestGeneratePrepareEnvInstructions(t *testing.T) {
	t.Setenv("PATH", "")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "RUNNER_") {
			t.Setenv(key, "")
		}
	}
	// The action passes the event inline and exports the short ref name.
	for key, value := range map[string]string{
		"GITHUB_ACTOR": "developer", "GITHUB_REPOSITORY": "org/repo",
		"GITHUB_EVENT_NAME": "issue_comment", "GITHUB_HEAD_REF": "", "GITHUB_REF_NAME": "main",
		"GITHUB_SHA": "checkout-sha", "GITHUB_SERVER_URL": "https://github.example", "GITHUB_RUN_ID": "42",
		"GITHUB_EVENT_PAYLOAD": `{"action":"created","issue":{"number":7,"title":"Fix the PR","body":"PR description","pull_request":{"html_url":"https://github.example/org/repo/pull/7"}},"comment":{"body":"@agent fix this"}}`,
	} {
		t.Setenv(key, value)
	}
	templateDir := filepath.Join("..", "template")
	cfg, err := config.LoadConfig(filepath.Join(templateDir, "go42x.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, ci, actions, ref string
		want, absent           []string
	}{
		{
			name: "action variables", ci: "true", actions: "true",
			want: []string{
				"CI: true", `CI environment value: "true"`, "Repository: org/repo", "Actor: developer",
				"Event: issue_comment", "Action: created", "Checkout ref: main", "Commit: checkout-sha",
				"Run: https://github.example/org/repo/actions/runs/42", "### Pull request #7",
				"Fix the PR", "PR description", "URL: https://github.example/org/repo/pull/7",
				"### Requested task", "@agent fix this",
			},
			absent: []string{"### Issue", "Source branch:"},
		},
		{
			name: "full ref available", ci: "true", actions: "true", ref: "refs/heads/main",
			want: []string{"Checkout ref: refs/heads/main"}, absent: []string{"Checkout ref: main"},
		},
		{
			name: "local generation", actions: "false",
			want: []string{"CI: false", `CI environment value: ""`}, absent: []string{"## GitHub Actions"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CI", tt.ci)
			t.Setenv("GITHUB_ACTIONS", tt.actions)
			t.Setenv("GITHUB_REF", tt.ref)
			out := t.TempDir()
			g := NewGenerator(slog.New(slog.DiscardHandler), cfg, templateDir, out)
			if err := g.Generate(t.Context(), false); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(out, "AGENTS.md"))
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			for _, want := range tt.want {
				if !strings.Contains(content, want) {
					t.Errorf("generated instructions missing %q", want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(content, absent) {
					t.Errorf("generated instructions contain unexpected %q", absent)
				}
			}
			if strings.Contains(content, "{{") || strings.Contains(content, "<no value>") {
				t.Error("generated instructions contain unresolved template values")
			}
		})
	}
}

func TestGenerateMCPConfigurations(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	cfg, err := config.LoadConfig(filepath.Join("..", "template", "go42x.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// This fixture exercises the JSON clients, including legacy SSE transport.
	delete(cfg.Providers, provider.Codex)
	enabled := true
	for name, p := range cfg.Providers {
		p.Enabled = &enabled
		cfg.Providers[name] = p
	}
	for name, s := range cfg.MCP {
		s.Enabled = true
		cfg.MCP[name] = s
	}
	cfg.MCP["sse"] = config.MCPServer{
		Enabled: true,
		Name:    "sse",
		Type:    "sse",
		URL:     "https://example.com/sse",
		Tools:   []string{"readItem"},
		Headers: map[string]string{"Authorization": "Bearer ${API_KEY}"},
	}
	cfg.MCP["local"] = config.MCPServer{
		Enabled: true,
		Name:    "local",
		Command: "example",
		Args:    []string{"--token=$TOKEN"},
		Tools:   []string{"tool.v2"},
		Env:     map[string]string{"TOKEN": "${TOKEN:-fallback}", "LITERAL": "true", "READY": "$COPILOT_MCP_READY"},
	}
	cfg.MCP["disabled"] = config.MCPServer{Name: "disabled", Command: "example", Tools: []string{"disabled_tool"}}
	original, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	g := NewGenerator(slog.New(slog.DiscardHandler), cfg, filepath.Join("..", "template"), out)
	if err := g.Generate(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	claude := readJSON[provider.ClaudeSettings](t, out, ".claude/settings.local.json")
	claudeMCP := readJSON[provider.ClaudeMCPConfig](t, out, ".mcp.json")
	gemini := readJSON[provider.GeminiSettings](t, out, ".gemini/settings.json")
	crush := readJSON[provider.CrushConfig](t, out, ".crush.json")
	copilot := readJSON[provider.CopilotMCPConfig](t, out, ".mcp.json")
	if len(claudeMCP.MCPServers) != 8 || len(gemini.MCPServers) != 8 || len(copilot.MCPServers) != 8 ||
		len(crush.MCP) != 8 {
		t.Fatal("enabled server set does not match generated configurations")
	}
	for name, s := range cfg.MCP {
		if !s.Enabled {
			continue
		}
		if claudeMCP.MCPServers[name].Type != s.Transport() {
			t.Errorf("Claude transport for %s", name)
		}
		if copilot.MCPServers[name].Type != s.Transport() {
			t.Errorf("Copilot transport for %s", name)
		}
		if !slices.Equal(copilot.MCPServers[name].Tools, []string{"*"}) {
			t.Errorf("Copilot must make all tools available for %s", name)
		}
		if !slices.Contains(claude.EnabledMCPServers, name) || !slices.Contains(gemini.MCP.Allowed, name) {
			t.Errorf("missing enabled server %s", name)
		}
		for _, tool := range s.Tools {
			if !slices.Contains(claude.Permissions.Allow, "mcp__"+name+"__"+tool) {
				t.Errorf("Claude permission missing for %s/%s", name, tool)
			}
			if !slices.Contains(crush.Permissions.AllowedTools, "mcp_"+name+"_"+tool) {
				t.Errorf("Crush permission missing for %s/%s", name, tool)
			}
		}
	}
	if _, ok := crush.MCP["gopls"]; !ok {
		t.Error("Crush gopls MCP server missing")
	}
	if crush.LSP["go"].Command != "gopls" {
		t.Error("Crush Go LSP missing")
	}
	if !slices.Equal(gemini.Tools.Allowed, cfg.Providers["gemini"].AutoApproveTools) {
		t.Error("Gemini built-ins mixed with MCP tools")
	}
	if !gemini.General.Checkpointing.Enabled || gemini.Privacy.UsageStatisticsEnabled {
		t.Error("Gemini checkpointing/privacy settings lost")
	}
	if gemini.MCPServers["jira"].HttpUrl != "https://mcp.atlassian.com/v2/mcp" || gemini.MCPServers["jira"].URL != "" {
		t.Error("HTTP endpoint must use httpUrl")
	}
	if gemini.MCPServers["sse"].URL != "https://example.com/sse" || gemini.MCPServers["sse"].HttpUrl != "" {
		t.Error("SSE endpoint must use url")
	}
	if gemini.MCPServers["sse"].Headers["Authorization"] != "Bearer ${API_KEY}" {
		t.Error("Gemini header changed")
	}
	if copilot.MCPServers["sse"].Headers["Authorization"] != "Bearer ${API_KEY}" {
		t.Error("Copilot header reference changed")
	}
	if copilot.MCPServers["github"].Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "${GH_MCP_KEY}" {
		t.Error("Copilot GitHub secret reference changed")
	}
	local := copilot.MCPServers["local"]
	if local.Env["TOKEN"] != "${TOKEN:-fallback}" || local.Env["LITERAL"] != "true" ||
		local.Env["READY"] != "$COPILOT_MCP_READY" ||
		local.Args[0] != "--token=$TOKEN" {
		t.Fatalf("Copilot environment references changed = %+v", local)
	}
	raw := readJSON[map[string]any](t, out, ".gemini/settings.json")
	for _, old := range []string{"coreTools", "excludeTools", "autoAccept", "allowMCPServers", "maxSessionTurns", "maxSessionDuration", "checkpointing", "usageStatisticsEnabled"} {
		if _, ok := raw[old]; ok {
			t.Errorf("obsolete Gemini key %s", old)
		}
	}
	data, err := os.ReadFile(filepath.Join(out, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "`kwb_search`") || strings.Contains(string(data), "{{") ||
		strings.Contains(string(data), "<no value>") {
		t.Error("shared instructions have unresolved template values or tool names")
	}
	for _, removed := range []string{"## Operational Modes", "## Workflows"} {
		if strings.Contains(string(data), removed) {
			t.Errorf("shared instructions still contain %s", removed)
		}
	}
	after, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("generation mutated shared config")
	}
	before := map[string]any{}
	for _, path := range []string{".claude/settings.local.json", ".mcp.json", ".gemini/settings.json", ".crush.json"} {
		before[path] = readJSON[any](t, out, path)
	}
	if err := g.Generate(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		if got := readJSON[any](t, out, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s generation is not deterministic", path)
		}
	}
}
