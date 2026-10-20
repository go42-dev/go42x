package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
)

func validConfig() *Config {
	return &Config{
		Version: "1.0",
		Project: Project{Name: "example"},
		Context: Context{Template: "agents.tpl.md"},
		Providers: map[string]Provider{
			"claude": {},
		},
		MCP: map[string]provider.MCPServer{
			"example": {Name: "example", Command: "example", Tools: []string{"search", "getIssue", "tool.v2-test"}},
		},
	}
}

func TestMCPContainerValidation(t *testing.T) {
	container := provider.MCPContainer{
		Image:   "go42x:test",
		Command: "go42x",
		Args:    []string{"mcp"},
	}
	for _, tt := range []struct {
		name, provider, transport string
		container                 *provider.MCPContainer
		want                      string
	}{
		{"valid", "gh-aw", provider.MCPServerTypeStdio, &container, ""},
		{"local provider", "codex", provider.MCPServerTypeStdio, &container, ""},
		{"no container", "gh-aw", provider.MCPServerTypeStdio, nil, ""},
		{"remote server", "gh-aw", provider.MCPServerTypeHTTP, &container, "only supported for stdio"},
		{"SSE server", "gh-aw", provider.MCPServerTypeSSE, &container, "only supported for stdio"},
		{"missing image", "gh-aw", provider.MCPServerTypeStdio, &provider.MCPContainer{Command: "go42x"}, "image and command are required"},
		{"missing command", "gh-aw", provider.MCPServerTypeStdio, &provider.MCPContainer{Image: "go42x:test"}, "image and command are required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Providers = map[string]Provider{
				tt.provider: {},
			}
			server := cfg.MCP["example"]
			server.Type = tt.transport
			server.Container = tt.container
			server.URL = "https://example.com/mcp"
			cfg.MCP["example"] = server
			err := cfg.Validate()
			if tt.want == "" && err != nil {
				t.Fatal(err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("Validate() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestProjectWithDefaults(t *testing.T) {
	for _, tt := range []struct {
		name, entrypoint, want string
	}{
		{"default", "", DefaultDocumentationEntrypoint},
		{"custom", "handbook/index.md", "handbook/index.md"},
		{"normalized", "./handbook/../team/index.markdown", "team/index.markdown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			project := Project{Repository: "git@example.com:org/repo.git", DocumentationEntrypoint: tt.entrypoint}
			effective := project.WithDefaults()
			if effective.DocumentationEntrypoint != tt.want || effective.Repository != project.Repository ||
				project.DocumentationEntrypoint != tt.entrypoint {
				t.Fatalf("effective project = %+v, original = %+v, want entrypoint %q", effective, project, tt.want)
			}
		})
	}
}

func TestValidateDocumentationEntrypoint(t *testing.T) {
	for _, tt := range []struct {
		name, entrypoint string
		valid            bool
	}{
		{"optional", "", true},
		{"custom Markdown", "handbook/index.md", true},
		{"Markdown extension", "team/start.markdown", true},
		{"normalized path", "./handbook/../team/start.md", true},
		{"uppercase extension", "docs/README.MD", true},
		{"blank", " \t", false},
		{"absolute", "/outside.md", false},
		{"outside root", "../outside.md", false},
		{"nested escape", "docs/../../outside.md", false},
		{"Windows path", `C:\docs\README.md`, false},
		{"backslash", `docs\README.md`, false},
		{"URL", "https://example.com/docs.md", false},
		{"NUL", "docs/\x00README.md", false},
		{"oversized", strings.Repeat("a", 4096) + ".md", false},
		{"directory", "docs", false},
		{"other format", "docs/index.txt", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Project.DocumentationEntrypoint = tt.entrypoint
			err := cfg.Validate()
			if (err == nil) != tt.valid ||
				(err != nil && !strings.Contains(err.Error(), "project.documentation-entrypoint")) {
				t.Fatalf("Validate() = %v, want valid %v", err, tt.valid)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"missing version", func(c *Config) { c.Version = "" }, "version is required"},
		{"future version", func(c *Config) { c.Version = "99.0" }, "unsupported configuration version"},
		{"old version", func(c *Config) { c.Version = "0.9" }, "unsupported configuration version"},
		{"malformed version", func(c *Config) { c.Version = "banana" }, "unsupported configuration version"},
		{"missing project", func(c *Config) { c.Project.Name = "" }, "project name is required"},
		{"missing providers", func(c *Config) { c.Providers = nil }, "at least one provider"},
		{
			"missing template",
			func(c *Config) { c.Context.Template = "" },
			"context.template is required",
		},
		{
			"missing server name",
			func(c *Config) { c.MCP["example"] = provider.MCPServer{Command: "example"} },
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
		server provider.MCPServer
		want   string
	}{
		{"implicit stdio", provider.MCPServer{Command: "go42x"}, ""},
		{"explicit stdio", provider.MCPServer{
			Type:    "stdio",
			Command: "go42x",
		}, ""},
		{"http", provider.MCPServer{
			Type: "http",
			URL:  "https://example.com/mcp",
		}, ""},
		{"sse", provider.MCPServer{
			Type: "sse",
			URL:  "http://localhost:9000/sse",
		}, ""},
		{"missing command", provider.MCPServer{}, "command is required"},
		{"blank command", provider.MCPServer{Command: "  "}, "command is required"},
		{"missing URL", provider.MCPServer{Type: "http"}, "valid HTTP(S) URL"},
		{"relative URL", provider.MCPServer{
			Type: "sse",
			URL:  "/sse",
		}, "valid HTTP(S) URL"},
		{"wrong URL scheme", provider.MCPServer{
			Type: "http",
			URL:  "file:///tmp/mcp",
		}, "valid HTTP(S) URL"},
		{"malformed URL", provider.MCPServer{
			Type: "http",
			URL:  "https://%",
		}, "valid HTTP(S) URL"},
		{"unknown transport", provider.MCPServer{Type: "websocket"}, "invalid type"},
		{"qualified name", provider.MCPServer{
			Command: "example",
			Tools:   []string{"mcp__example__search"},
		}, "raw MCP tool name"},
		{"empty tool", provider.MCPServer{
			Command: "example",
			Tools:   []string{""},
		}, "invalid tool"},
		{"tool whitespace", provider.MCPServer{
			Command: "example",
			Tools:   []string{"get issue"},
		}, "invalid tool"},
		{"long tool", provider.MCPServer{
			Command: "example",
			Tools:   []string{strings.Repeat("a", 129)},
		}, "invalid tool"},
		{"duplicate tool", provider.MCPServer{
			Command: "example",
			Tools:   []string{"search", "search"},
		}, "duplicate tool"},
		{"case sensitive names", provider.MCPServer{
			Command: "example",
			Tools:   []string{"search", "Search"},
		}, ""},
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
	if got := (provider.MCPServer{}).Transport(); got != "stdio" {
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

func TestLoadConfigSingleDocument(t *testing.T) {
	data, err := yaml.Marshal(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, prefix, suffix, want string }{
		{"single document", "", "", ""},
		{"trailing comments", "", "\n# trailing comment\n\n", ""},
		{"explicit document markers", "---\n", "...\n# trailing comment\n", ""},
		{"second mapping", "", "---\nproject: {name: ignored}\n", "exactly one YAML document"},
		{"second scalar", "", "---\nignored\n", "exactly one YAML document"},
		{"second empty document", "", "---\n", "exactly one YAML document"},
		{"second null document", "", "---\nnull\n", "exactly one YAML document"},
		{"malformed second document", "", "---\nversion: [\n", "failed to parse YAML"},
		{"malformed trailing content", "", "...\ninvalid trailing content\n", "failed to parse YAML"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			if err := os.WriteFile(path, []byte(tt.prefix+string(data)+tt.suffix), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tt.want == "" {
				if err != nil || cfg == nil || cfg.Project.Name != "example" {
					t.Fatalf("LoadConfig() = %+v, %v; want the single project configuration", cfg, err)
				}
			} else if cfg != nil || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig() = %+v, %v; want no config and %q", cfg, err, tt.want)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	templateDir := filepath.Join("..", "..", "assets", "agentenv")
	path := filepath.Join(templateDir, "go42x.yaml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 6 || len(cfg.MCP) != 7 {
		t.Fatalf("default providers/servers = %d/%d", len(cfg.Providers), len(cfg.MCP))
	}
	paths := []string{cfg.Context.Template, cfg.Context.ChunksDir}
	for _, p := range cfg.Providers {
		paths = append(paths, p.Agents...)
	}
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(templateDir, path)); err != nil {
			t.Errorf("missing template path %s: %v", path, err)
		}
	}
	for name, s := range cfg.MCP {
		if len(s.Tools) == 0 {
			t.Errorf("server %s has no tools", name)
		}
		if s.Enabled != (name == "go42x" || name == "gopls" || name == "mise") {
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

func TestLoadConfigRejectsRemovedContextDirectories(t *testing.T) {
	for _, field := range []string{"modes-dir", "workflows-dir"} {
		t.Run(field, func(t *testing.T) {
			data := []byte("version: '1.0'\nproject: {name: example}\n" +
				"context: {template: agents.tpl.md, " + field + ": old/}\nproviders: {codex: {}}\n")
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), field) {
				t.Errorf("LoadConfig() = %v, want rejection of %s", err, field)
			}
		})
	}
}

func TestValidateGoEnv(t *testing.T) {
	for _, tt := range []struct {
		name  string
		valid bool
	}{
		{"GOFLAGS", true}, {"GOPRIVATE", true}, {"CC", true}, {"CGO_ENABLED", true},
		{"", false}, {"gopath", false}, {"-w", false}, {"GOFLAGS=-x", false}, {"GO FLAGS", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Context.GoEnv = []string{tt.name}
			cfg.MCP = nil
			if err := cfg.Validate(); (err == nil) != tt.valid {
				t.Errorf("Validate() = %v, want valid %v", err, tt.valid)
			}
		})
	}
}

func TestLoadConfigTempDirectoryValidation(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		valid      bool
	}{
		{"empty", "", true},
		{"default", ".build", true},
		{"relative", "tmp/agent outputs", true},
		{"absolute", "/tmp/agent-output", true},
		{"blank", " \t ", false},
		{"NUL", "tmp\x00outputs", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := fmt.Appendf(nil, "version: '1.0'\nproject: {name: example}\n"+
				"context: {template: agents.tpl.md, temp-dir: %q}\nproviders: {codex: {}}\n", tt.path)
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); (err == nil) != tt.valid {
				t.Errorf("LoadConfig() = %v, want valid %v", err, tt.valid)
			}
		})
	}
}

func TestLoadConfigSupportedVersions(t *testing.T) {
	for _, version := range []string{"1.0", "0.9", "1.1", "99.0", "1", "banana", "", " 1.0 "} {
		t.Run(version, func(t *testing.T) {
			data := fmt.Appendf(nil, "version: %q\nproject: {name: example}\n"+
				"context: {template: agents.tpl.md}\nproviders: {codex: {}}\n", version)
			wantErr := version != "1.0"
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); (err != nil) != wantErr {
				t.Errorf("LoadConfig() = %v, want error %v", err, wantErr)
			}
		})
	}
}

func TestLoadConfigProviderEnabled(t *testing.T) {
	for _, tt := range []struct {
		name, entry string
		want        bool
	}{
		{"omitted", "{}", true},
		{"enabled", "{enabled: true}", true},
		{"disabled", "{enabled: false}", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			data := "version: '1.0'\nproject: {name: example}\ncontext: {template: agents.tpl.md}\nproviders:\n"
			for _, name := range []string{"claude", "codex", "antigravity", "crush", "copilot", "gh-aw"} {
				data += "  " + name + ": " + tt.entry + "\n"
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			for name := range cfg.Providers {
				if got := cfg.ProviderEnabled(name); got != tt.want {
					t.Errorf("ProviderEnabled(%q) = %v, want %v", name, got, tt.want)
				}
			}
			delete(cfg.Providers, "claude")
			if cfg.ProviderEnabled("claude") {
				t.Error("absent provider is enabled")
			}
		})
	}
}

func validProviderConfig() *Config {
	return &Config{
		Version: "1.0",
		Project: Project{Name: "example"},
		Context: Context{Template: "agents.tpl.md"},
		Providers: map[string]Provider{
			claude.Name: {},
		},
		MCP: map[string]provider.MCPServer{
			"example": {
				Name:    "example",
				Command: "example",
				Tools:   []string{"search", "getIssue", "tool.v2-test"},
			},
		},
	}
}

func TestValidateLoadedConfig(t *testing.T) {
	const base = `version: "1.0"
project: {name: example}
context: {template: agents.tpl.md}
`
	for _, tt := range []struct {
		name, content string
		wantErr       bool
	}{
		{"provider enabled by default", "providers: {codex: {}}", false},
		{"disabled provider", "providers: {codex: {enabled: false}}", false},
		{"missing providers", "", true},
		{"empty providers", "providers: {}", true},
		{"unknown provider", "providers: {unknown: {}}", true},
		{"retired Gemini provider", "providers: {gemini: {}}", true},
		{"Antigravity provider", "providers: {antigravity: {}}", false},
		{"disabled Antigravity", "providers: {antigravity: {enabled: false}}", false},
		{"Antigravity legacy tool approvals", "providers: {antigravity: {auto-approve-tools: []}}", true},
		{"Antigravity approval policy", "providers: {antigravity: {approval-policy: never}}", true},
		{"Antigravity MCP approvals", "providers: {antigravity: {mcp-approval: auto}}", true},
		{"unsupported provider option", "providers: {copilot: {approval-policy: never}}", true},
		{
			name:    "workflow bindings",
			content: "providers: {gh-aw: {bindings: {TOKEN: '${{ github.token }}', MODEL: '${{ vars.MODEL }}'}}}",
		},
		{
			name:    "workflow empty bindings",
			content: "providers: {gh-aw: {bindings: {}}}",
		},
		{
			name:    "bindings on another provider",
			content: "providers: {codex: {bindings: {TOKEN: value}}}",
			wantErr: true,
		},
		{
			name:    "empty bindings on another provider",
			content: "providers: {copilot: {bindings: {}}}",
			wantErr: true,
		},
		{
			name:    "invalid binding name",
			content: "providers: {gh-aw: {bindings: {bad-name: value}}}",
			wantErr: true,
		},
		{
			name:    "empty binding name",
			content: "providers: {gh-aw: {bindings: {'': value}}}",
			wantErr: true,
		},
		{
			name:    "blank binding",
			content: "providers: {gh-aw: {bindings: {TOKEN: ' '}}}",
			wantErr: true,
		},
		{
			name:    "binding must be a string",
			content: "providers: {gh-aw: {bindings: {TOKEN: []}}}",
			wantErr: true,
		},
		{"unsupported agents", "providers: {codex: {agents: []}}", true},
		{"blank approved tool", "providers: {claude: {auto-approve-tools: [' ']}}", true},
		{"invalid approval policy", "providers: {codex: {approval-policy: invalid}}", true},
		{"invalid MCP approval", "providers: {codex: {mcp-approval: invalid}}", true},
		{
			"raw MCP tool names",
			`providers: {codex: {}}
mcp: {server: {name: server, command: go42x, tools: [search, tool.v2-test, find_mcp__server]}}`,
			false,
		},
		{
			"qualified MCP tool name",
			`providers: {codex: {}}
mcp: {server: {name: server, command: go42x, tools: [mcp__server__search]}}`,
			true,
		},
		{
			"cwd containing NUL",
			`providers: {codex: {}}
mcp: {server: {name: server, command: go42x, cwd: "src\0"}}`,
			true,
		},
		{
			"cwd containing environment variable",
			`providers: {codex: {}}
mcp: {server: {name: server, command: go42x, cwd: '${PROJECT_DIR}/src'}}`,
			true,
		},
		{
			"cwd containing home expansion",
			`providers: {codex: {}}
mcp: {server: {name: server, command: go42x, cwd: '~/src'}}`,
			true,
		},
		{
			"empty cwd",
			`providers: {codex: {}}
mcp: {server: {name: server, command: go42x, cwd: ''}}`,
			false,
		},
		{
			"cwd with implicitly enabled claude",
			`providers: {claude: {}}
mcp: {server: {enabled: true, name: server, command: go42x, cwd: src}}`,
			true,
		},
		{
			"cwd with disabled claude",
			`providers: {claude: {enabled: false}, codex: {}}
mcp: {server: {enabled: true, name: server, command: go42x, cwd: src}}`,
			false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(base + tt.content)
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestDisabledProvidersAllowMCPCWD(t *testing.T) {
	enabled, disabled := true, false
	for _, name := range []string{"claude", "crush", ghaw.Name} {
		for _, tt := range []struct {
			name    string
			enabled *bool
			wantErr bool
		}{
			{"omitted", nil, true},
			{"enabled", &enabled, true},
			{"disabled", &disabled, false},
		} {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				cfg := validProviderConfig()
				cfg.Providers = map[string]Provider{
					"copilot": {},
					name:      {Enabled: tt.enabled},
				}
				cfg.MCP["example"] = provider.MCPServer{
					Enabled: true,
					Name:    "example",
					Command: "example",
					CWD:     "src",
				}
				err := cfg.Validate()
				if tt.wantErr {
					if err == nil || !strings.Contains(err.Error(), "provider "+name+" does not support cwd") {
						t.Fatalf("Validate() = %v, want unsupported cwd error", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestProjectConfigValidationAfterProviderSelection(t *testing.T) {
	const base = `version: "1.0"
project: {name: example}
context: {template: agents.tpl.md}
providers:
  claude: {}
  codex: {}
mcp:
  server: {enabled: true, name: server, command: example, cwd: src}
`
	for _, tt := range []struct {
		name, local, want string
		selection         []string
	}{
		{
			name: "configured incompatible provider",
			want: "provider claude does not support cwd",
		},
		{
			name:      "exact selection disables incompatible provider",
			selection: []string{codex.Name},
		},
		{
			name:  "local override disables incompatible provider",
			local: "providers: {claude: {enabled: false}}",
		},
		{
			name:      "unselected unknown provider is rejected",
			local:     "providers: {typo: {enabled: false}}",
			selection: []string{codex.Name},
			want:      "unknown provider",
		},
		{
			name:      "invalid effective provider option",
			local:     "providers: {codex: {approval-policy: invalid}}",
			selection: []string{codex.Name},
			want:      "invalid approval-policy",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProjectConfig([]byte(base), []byte(tt.local), tt.selection)
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseProjectConfig() = %v, want %q", err, tt.want)
			}
		})
	}
}
