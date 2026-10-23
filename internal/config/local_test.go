package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
)

const sharedConfig = `version: "1.0"
project:
  name: shared
  tags: [one, two]
  repository: github.com/example/shared
  documentation-entrypoint: docs/shared.md
  metadata: {keep: value, change: old}
env: [SHARED]
context: {template: agents.tpl.md, chunks-dir: chunks/}
providers:
  claude: {enabled: true, auto-approve-tools: [Read, Bash]}
  codex: {enabled: false, approval-policy: on-request, mcp-approval: prompt}
mcp:
  server:
    enabled: true
    name: server
    command: task
    args: [tool, --, gopls, mcp]
    env: {KEEP: shared, CHANGE: old}
`

func writeConfigFixture(t *testing.T, shared, local string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "go42x.yaml")
	if err := os.WriteFile(path, []byte(shared), 0600); err != nil {
		t.Fatal(err)
	}
	if len(local) > 0 {
		if err := os.WriteFile(filepath.Join(dir, FileLocal), []byte(local), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestProjectConfigContainerOverrides(t *testing.T) {
	shared := sharedConfig + `    container:
      image: example/tools
      version: v1
      command: gopls
      args: [mcp]
`
	for _, tt := range []struct {
		name  string
		local string
		want  *provider.MCPContainer
	}{
		{
			"inherit",
			"",
			&provider.MCPContainer{
				Image:   "example/tools",
				Version: "v1",
				Command: "gopls",
				Args:    []string{"mcp"},
			},
		},
		{
			"override",
			"mcp: {server: {container: {version: v2}}}",
			&provider.MCPContainer{
				Image:   "example/tools",
				Version: "v2",
				Command: "gopls",
				Args:    []string{"mcp"},
			},
		},
		{
			"clear",
			"mcp: {server: {container: null}}",
			nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadProjectConfig(writeConfigFixture(t, shared, tt.local), nil)
			if err != nil {
				t.Fatal(err)
			}
			server := cfg.MCP["server"]

			if !reflect.DeepEqual(server.Container, tt.want) || server.Command != "task" {
				t.Fatalf("merged MCP server = %+v, want container %+v and local task command", server, tt.want)
			}
		})
	}
}

func TestProjectConfigGoEnv(t *testing.T) {
	shared := strings.Replace(sharedConfig, "chunks-dir: chunks/", "chunks-dir: chunks/, go-env: [GOFLAGS]", 1)
	for _, tt := range []struct {
		name  string
		local string
		want  []string
	}{
		{
			"shared",
			"",
			[]string{"GOFLAGS"},
		},
		{
			"override",
			"context: {go-env: [CC, GOPRIVATE]}",
			[]string{"CC", "GOPRIVATE"},
		},
		{
			"clear",
			"context: {go-env: []}",
			nil,
		},
		{
			"null",
			"context: {go-env: null}",
			nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadProjectConfig(writeConfigFixture(t, shared, tt.local), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cfg.Context.GoEnv, tt.want) || cfg.Context.Template != "agents.tpl.md" {
				t.Fatalf("merged context = %+v, want Go env %v and inherited template", cfg.Context, tt.want)
			}
		})
	}
}

func TestProjectConfigMergeAndSourcePreservation(t *testing.T) {
	const local = `project:
  tags: []
  repository: github.com/example/local
  documentation-entrypoint: team/index.md
  metadata: {change: local}
env: []
context: {chunks-dir: custom/}
providers:
  claude: {enabled: false, auto-approve-tools: []}
  codex: {enabled: true, approval-policy: null}
mcp:
  server: {enabled: false, args: [mcp], env: {CHANGE: local}}
`
	path := writeConfigFixture(t, sharedConfig, local)
	cfg, err := ParseProjectConfig([]byte(sharedConfig), []byte(local), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProjectConfig(path, nil)
	if err != nil || !reflect.DeepEqual(cfg, loaded) {
		t.Fatalf("file and byte configuration loaders differ: %+v, %v", loaded, err)
	}
	if cfg.Project.Name != "shared" || len(cfg.Project.Tags) != 0 || len(cfg.EnvVars) != 0 ||
		cfg.Project.Repository != "github.com/example/local" ||
		cfg.Project.DocumentationEntrypoint != "team/index.md" ||
		!reflect.DeepEqual(cfg.Project.Metadata, map[string]string{
			"keep":   "value",
			"change": "local",
		}) {
		t.Fatalf("incorrect project merge: %+v", cfg)
	}
	if cfg.Context.Template != "agents.tpl.md" || cfg.Context.ChunksDir != "custom/" {
		t.Fatalf("incorrect context merge: %+v", cfg.Context)
	}
	if cfg.ProviderEnabled("claude") || !cfg.ProviderEnabled("codex") ||
		len(cfg.Providers["claude"].AutoApproveTools) != 0 || cfg.Providers["codex"].ApprovalPolicy != nil ||
		*cfg.Providers["codex"].MCPApproval != "prompt" {
		t.Fatalf("incorrect provider merge: %+v", cfg.Providers)
	}
	server := cfg.MCP["server"]

	if server.Enabled || server.Name != "server" || server.Command != "task" ||
		!slices.Equal(server.Args, []string{"mcp"}) ||
		!reflect.DeepEqual(server.Env, map[string]string{
			"KEEP":   "shared",
			"CHANGE": "local",
		}) {
		t.Fatalf("incorrect MCP merge: %+v", server)
	}
	for name, want := range map[string]string{
		"go42x.yaml": sharedConfig,
		FileLocal:    local,
	} {
		got, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
		if err != nil || !bytes.Equal(got, []byte(want)) {
			t.Fatalf("source changed: %s: %v", name, err)
		}
	}
}

func TestProjectConfigExactProviderSelection(t *testing.T) {
	path := writeConfigFixture(t, sharedConfig, "providers: {codex: {enabled: true}}\n")
	for _, tt := range []struct {
		name      string
		selection []string
		want      []string
	}{
		{
			"configuration fallback",
			nil,
			[]string{"claude", "codex"},
		},
		{
			"override local and project",
			[]string{"codex"},
			[]string{"codex"},
		},
		{
			"supported but unconfigured",
			[]string{"antigravity"},
			[]string{"antigravity"},
		},
		{
			"agentic workflow fragment",
			[]string{"gh-aw"},
			[]string{"gh-aw"},
		},
		{
			"multiple",
			[]string{"codex", "copilot"},
			[]string{"codex", "copilot"},
		},
		{
			"explicit none",
			[]string{},
			[]string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadProjectConfig(path, tt.selection)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"claude", "codex", "antigravity", "crush", "copilot", "gh-aw"} {
				if cfg.ProviderEnabled(name) != slices.Contains(tt.want, name) {
					t.Errorf("provider %s enabled = %v; selection %v", name, cfg.ProviderEnabled(name), tt.selection)
				}
			}
			if *cfg.Providers["codex"].MCPApproval != "prompt" {
				t.Fatal("selection discarded the provider's settings")
			}
		})
	}
}

func TestProjectConfigWithoutLocalOverrides(t *testing.T) {
	shared := strings.Replace(sharedConfig,
		"template: agents.tpl.md", "template: '${GO42X_PROJECT_ROOT}/agents.tpl.md'", 1)
	tests := []struct {
		name      string
		local     string
		directory bool
	}{
		{
			name: "valid overrides",
			local: `project: {name: local}
context: {template: local.tpl.md}
providers: {codex: {enabled: true}}
mcp: {server: {enabled: false}}
`,
		},
		{
			name:  "invalid YAML",
			local: "project: [",
		},
		{
			name:  "unknown built-in",
			local: "project: {name: '${GO42X_UNKNOWN}'}",
		},
		{
			name:      "unreadable local file",
			directory: true,
		},
		{
			name: "missing local file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFixture(t, shared, tt.local)
			localPath := filepath.Join(filepath.Dir(path), FileLocal)
			if tt.directory {
				if err := os.Mkdir(localPath, 0700); err != nil {
					t.Fatal(err)
				}
			}

			cfg, err := LoadProjectConfig(path, []string{"gh-aw"}, WithLocalOverrides(false))

			if err != nil {
				t.Fatal(err)
			}
			if cfg.Project.Name != "shared" || !cfg.MCP["server"].Enabled {
				t.Fatalf("local overrides affected shared configuration: %+v", cfg)
			}
			if want := filepath.Join(filepath.Dir(path), "agents.tpl.md"); cfg.Context.Template != want {
				t.Errorf("template = %q, want resolved shared path %q", cfg.Context.Template, want)
			}
			if !cfg.ProviderEnabled("gh-aw") || cfg.ProviderEnabled("claude") || cfg.ProviderEnabled("codex") {
				t.Errorf("provider selection was not applied: %+v", cfg.Providers)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != shared {
				t.Fatalf("shared source changed: %s, %v", data, err)
			}
			if len(tt.local) > 0 {
				if data, err := os.ReadFile(localPath); err != nil || string(data) != tt.local {
					t.Fatalf("local source changed: %s, %v", data, err)
				}
			}
		})
	}
}

func TestProjectConfigWithoutLocalOverridesValidatesSharedConfig(t *testing.T) {
	for _, tt := range []struct {
		name   string
		shared string
		want   string
	}{
		{
			name:   "unknown field",
			shared: sharedConfig + "unknown: true\n",
			want:   "project configuration",
		},
		{
			name:   "invalid version",
			shared: strings.Replace(sharedConfig, `version: "1.0"`, `version: "2.0"`, 1),
			want:   "unsupported configuration version",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFixture(t, tt.shared, "project: [")

			_, err := LoadProjectConfig(path, nil, WithLocalOverrides(false))

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadProjectConfig() = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestProjectConfigMissingAndInvalidFiles(t *testing.T) {
	path := writeConfigFixture(t, sharedConfig, "")
	legacy, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := LoadProjectConfig(path, nil)
	if err != nil || !reflect.DeepEqual(legacy, effective) {
		t.Fatalf("missing local file changed legacy behavior: %+v, %v", effective, err)
	}
	if _, err := LoadProjectConfig(filepath.Join(t.TempDir(), "missing.yaml"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing shared config: %v", err)
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(path), FileLocal), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectConfig(path, nil); err == nil || !strings.Contains(err.Error(), "local configuration") {
		t.Fatalf("unreadable local config was ignored: %v", err)
	}
	for _, tt := range []struct {
		name  string
		local string
		want  string
	}{
		{
			"bad YAML",
			"providers: [",
			"failed to parse YAML",
		},
		{
			"unknown field",
			"providers: {codex: {enable: true}}",
			"field enable",
		},
		{
			"wrong value type",
			"providers: {codex: {enabled: definitely}}",
			"cannot unmarshal",
		},
		{
			"duplicate keys",
			"env: []\nenv: []",
			"already defined",
		},
		{
			"extra document",
			"providers: {}\n---\n",
			"exactly one YAML document",
		},
		{
			"clear required field",
			"context: {template: null}",
			"context.template is required",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFixture(t, sharedConfig, tt.local)
			if _, err := LoadProjectConfig(
				path,
				[]string{"codex"},
			); err == nil ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadProjectConfig() = %v, want %s", err, tt.want)
			}
		})
	}
	path = writeConfigFixture(t, sharedConfig+"mistake: true\n", "mistake: null\n")
	if _, err := LoadProjectConfig(path, nil); err == nil || !strings.Contains(err.Error(), "project configuration") {
		t.Fatalf("local file hid a shared configuration error: %v", err)
	}
}
