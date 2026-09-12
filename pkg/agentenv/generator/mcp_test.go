package generator

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/provider"
)

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

func TestGenerateMCPConfigurations(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	cfg, err := config.LoadConfig(filepath.Join("..", "template", "go42x.yaml"))
	if err != nil {
		t.Fatal(err)
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
		CWD:     "/project",
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
	if err := g.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	claude := readJSON[provider.ClaudeSettings](t, out, ".claude/settings.json")
	claudeMCP := readJSON[provider.ClaudeMCPConfig](t, out, ".mcp.json")
	gemini := readJSON[provider.GeminiSettings](t, out, ".gemini/settings.json")
	crush := readJSON[provider.CrushConfig](t, out, ".crush.json")
	copilot := readJSON[provider.CopilotMCPConfig](t, out, ".github/.copilot.mcp.json")
	if len(claudeMCP.MCPServers) != 7 || len(gemini.MCPServers) != 7 || len(copilot.MCPServers) != 7 ||
		len(crush.MCP) != 6 {
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
		if !slices.Equal(gemini.MCPServers[name].IncludeTools, s.Tools) {
			t.Errorf("Gemini must filter raw tools for %s", name)
		}
		if !slices.Equal(copilot.MCPServers[name].Tools, s.Tools) {
			t.Errorf("Copilot must filter raw tools for %s", name)
		}
		if !slices.Contains(claude.EnabledMCPServers, name) || !slices.Contains(gemini.MCP.Allowed, name) {
			t.Errorf("missing enabled server %s", name)
		}
		for _, tool := range s.Tools {
			if !slices.Contains(claude.Permissions.Allow, "mcp__"+name+"__"+tool) {
				t.Errorf("Claude permission missing for %s/%s", name, tool)
			}
			if name != "gopls" && !slices.Contains(crush.Permissions.AllowedTools, "mcp_"+name+"_"+tool) {
				t.Errorf("Crush permission missing for %s/%s", name, tool)
			}
		}
	}
	if _, ok := crush.MCP["gopls"]; ok {
		t.Error("Crush must keep its built-in gopls setup")
	}
	if crush.LSP["go"].Command != "gopls" {
		t.Error("Crush Go LSP missing")
	}
	if !slices.Equal(gemini.Tools.Core, cfg.Providers["gemini"].Tools) ||
		!slices.Equal(gemini.Tools.Allowed, cfg.Providers["gemini"].Tools) {
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
	if gemini.MCPServers["local"].CWD != "/project" {
		t.Error("Gemini working directory lost")
	}
	if gemini.MCPServers["sse"].Headers["Authorization"] != "Bearer ${API_KEY}" {
		t.Error("Gemini header changed")
	}
	if copilot.MCPServers["sse"].Headers["Authorization"] != "Bearer ${COPILOT_MCP_API_KEY}" {
		t.Error("Copilot header reference missing prefix")
	}
	if copilot.MCPServers["github"].Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "${COPILOT_MCP_GH_MCP_KEY}" {
		t.Error("Copilot GitHub secret reference missing prefix")
	}
	local := copilot.MCPServers["local"]
	if local.Env["TOKEN"] != "${COPILOT_MCP_TOKEN:-fallback}" || local.Env["LITERAL"] != "true" ||
		local.Env["READY"] != "$COPILOT_MCP_READY" ||
		local.Args[0] != "--token=$COPILOT_MCP_TOKEN" {
		t.Fatalf("Copilot secret conversion = %+v", local)
	}
	raw := readJSON[map[string]any](t, out, ".gemini/settings.json")
	for _, old := range []string{"coreTools", "excludeTools", "autoAccept", "allowMCPServers", "maxSessionTurns", "maxSessionDuration", "checkpointing", "usageStatisticsEnabled"} {
		if _, ok := raw[old]; ok {
			t.Errorf("obsolete Gemini key %s", old)
		}
	}
	for name, want := range map[string]string{"claude": "mcp__go42x__kwb_search", "gemini": "mcp_go42x_kwb_search", "crush": "mcp_go42x_kwb_search", "copilot": "go42x/kwb_search"} {
		data, err := os.ReadFile(filepath.Join(out, cfg.Providers[name].Output))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) || strings.Contains(string(data), "{{") ||
			strings.Contains(string(data), "<no value>") {
			t.Errorf("%s instructions have unresolved template values or tool names", name)
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
	for _, path := range []string{".claude/settings.json", ".mcp.json", ".gemini/settings.json", ".crush.json", ".github/.copilot.mcp.json"} {
		before[path] = readJSON[any](t, out, path)
	}
	if err := g.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		if got := readJSON[any](t, out, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s generation is not deterministic", path)
		}
	}
}
