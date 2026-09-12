package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func validConfig() *Config {
	return &Config{Version: "1.0", Project: Project{Name: "example"}, Providers: map[string]Provider{
		"claude": {Template: "claude.tpl.md", Output: "CLAUDE.md"},
	}, MCP: map[string]MCPServer{"example": {Name: "example", Command: "example", Tools: []string{"search", "getIssue", "tool.v2-test"}}}}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"missing version", func(c *Config) { c.Version = "" }, "version is required"},
		{"missing project", func(c *Config) { c.Project.Name = "" }, "project name is required"},
		{"missing providers", func(c *Config) { c.Providers = nil }, "at least one provider"},
		{"unknown provider", func(c *Config) { c.Providers = map[string]Provider{"typo": {}} }, "unknown provider"},
		{
			"missing template",
			func(c *Config) { c.Providers["claude"] = Provider{Output: "out"} },
			"template is required",
		},
		{"missing output", func(c *Config) { c.Providers["claude"] = Provider{Template: "in"} }, "output is required"},
		{
			"missing server name",
			func(c *Config) { c.MCP["example"] = MCPServer{Command: "example"} },
			"name is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.change(cfg)
			err := cfg.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want %q", err, tt.want)
			}
		})
	}
	var cfg *Config
	if err := cfg.Validate(); err == nil {
		t.Fatal("nil config accepted")
	}
}

func TestValidateMCPServers(t *testing.T) {
	for _, tt := range []struct {
		name   string
		server MCPServer
		want   string
	}{
		{"implicit stdio", MCPServer{Command: "go42x"}, ""},
		{"explicit stdio", MCPServer{Type: "stdio", Command: "go42x"}, ""},
		{"http", MCPServer{Type: "http", URL: "https://example.com/mcp"}, ""},
		{"sse", MCPServer{Type: "sse", URL: "http://localhost:9000/sse"}, ""},
		{"missing command", MCPServer{}, "command is required"},
		{"blank command", MCPServer{Command: "  "}, "command is required"},
		{"missing URL", MCPServer{Type: "http"}, "valid HTTP(S) URL"},
		{"relative URL", MCPServer{Type: "sse", URL: "/sse"}, "valid HTTP(S) URL"},
		{"wrong URL scheme", MCPServer{Type: "http", URL: "file:///tmp/mcp"}, "valid HTTP(S) URL"},
		{"malformed URL", MCPServer{Type: "http", URL: "https://%"}, "valid HTTP(S) URL"},
		{"unknown transport", MCPServer{Type: "websocket"}, "invalid type"},
		{"qualified name", MCPServer{Command: "example", Tools: []string{"mcp__example__search"}}, "raw MCP tool name"},
		{"empty tool", MCPServer{Command: "example", Tools: []string{""}}, "invalid tool"},
		{"tool whitespace", MCPServer{Command: "example", Tools: []string{"get issue"}}, "invalid tool"},
		{"long tool", MCPServer{Command: "example", Tools: []string{strings.Repeat("a", 129)}}, "invalid tool"},
		{"duplicate tool", MCPServer{Command: "example", Tools: []string{"search", "search"}}, "duplicate tool"},
		{"case sensitive names", MCPServer{Command: "example", Tools: []string{"search", "Search"}}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.server.Name = "example"
			cfg.MCP["example"] = tt.server
			err := cfg.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "MCP server example:") ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want server context and %q", err, tt.want)
			}
		})
	}
	if got := (MCPServer{}).Transport(); got != "stdio" {
		t.Fatalf("default transport = %q", got)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go42x.yaml")
	if _, err := LoadConfig(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
	for _, tt := range []struct{ name, content, want string }{
		{"malformed YAML", "version: [", "failed to parse YAML"},
		{"invalid config", "version: '1.0'", "invalid config: project name"},
		{"empty file", "", "invalid config: version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig() = %v, want %q", err, tt.want)
			}
		})
	}
	data, err := yaml.Marshal(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "1.0" || cfg.MCP["example"].Transport() != "stdio" {
		t.Fatalf("loaded config = %+v", cfg)
	}
}

func TestDefaultConfig(t *testing.T) {
	path := filepath.Join("..", "template", "go42x.yaml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 4 || len(cfg.MCP) != 5 {
		t.Fatalf("default providers/servers = %d/%d", len(cfg.Providers), len(cfg.MCP))
	}
	for name, p := range cfg.Providers {
		files := append([]string{p.Template}, p.Chunks...)
		files = append(files, p.Modes...)
		files = append(files, p.Workflows...)
		files = append(files, p.Agents...)
		for _, file := range files {
			if _, err := os.Stat(filepath.Join("..", "template", file)); err != nil {
				t.Errorf("provider %s references missing template %s: %v", name, file, err)
			}
		}
	}
	for name, s := range cfg.MCP {
		if len(s.Tools) == 0 {
			t.Errorf("server %s has no tools", name)
		}
		if s.Enabled != (name == "go42x" || name == "gopls") {
			t.Errorf("unexpected enabled state for %s", name)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["version"].(string); !ok {
		t.Fatal("version must be a YAML string")
	}
	for name, entry := range raw["mcp"].(map[string]any) {
		env, _ := entry.(map[string]any)["env"].(map[string]any)
		for key, value := range env {
			if _, ok := value.(string); !ok {
				t.Errorf("%s.env.%s must be a YAML string", name, key)
			}
		}
	}
}
