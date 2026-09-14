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
)

const sharedConfig = `version: "1.0"
project: {name: shared, tags: [one, two], metadata: {keep: value, change: old}}
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
	if local != "" {
		if err := os.WriteFile(filepath.Join(dir, LocalConfigFile), []byte(local), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestProjectConfigMergeAndSourcePreservation(t *testing.T) {
	const local = `project: {tags: [], metadata: {change: local}}
env: []
context: {chunks-dir: custom/}
providers:
  claude: {enabled: false, auto-approve-tools: []}
  codex: {enabled: true, approval-policy: null}
mcp:
  server: {enabled: false, args: [mcp], env: {CHANGE: local}}
`
	path := writeConfigFixture(t, sharedConfig, local)
	cfg, err := LoadProjectConfig(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Name != "shared" || len(cfg.Project.Tags) != 0 || len(cfg.EnvVars) != 0 ||
		!reflect.DeepEqual(cfg.Project.Metadata, map[string]string{"keep": "value", "change": "local"}) {
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
		!reflect.DeepEqual(server.Env, map[string]string{"KEEP": "shared", "CHANGE": "local"}) {
		t.Fatalf("incorrect MCP merge: %+v", server)
	}
	for name, want := range map[string]string{"go42x.yaml": sharedConfig, LocalConfigFile: local} {
		got, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
		if err != nil || !bytes.Equal(got, []byte(want)) {
			t.Fatalf("source changed: %s: %v", name, err)
		}
	}
}

func TestProjectConfigExactProviderSelection(t *testing.T) {
	path := writeConfigFixture(t, sharedConfig, "providers: {codex: {enabled: true}}\n")
	for _, tt := range []struct {
		name            string
		selection, want []string
	}{
		{"configuration fallback", nil, []string{"claude", "codex"}},
		{"override local and project", []string{"codex"}, []string{"codex"}},
		{"supported but unconfigured", []string{"gemini"}, []string{"gemini"}},
		{"multiple", []string{"codex", "copilot"}, []string{"codex", "copilot"}},
		{"explicit none", []string{}, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadProjectConfig(path, tt.selection)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"claude", "codex", "gemini", "crush", "copilot"} {
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

func TestProjectConfigCompatibilityValidationAfterSelection(t *testing.T) {
	path := writeConfigFixture(t, sharedConfig, "mcp: {server: {cwd: src}}\n")
	if _, err := LoadProjectConfig(path, nil); err == nil || !strings.Contains(err.Error(), "does not support cwd") {
		t.Fatalf("enabled incompatible provider: %v", err)
	}
	if _, err := LoadProjectConfig(path, []string{"codex"}); err != nil {
		t.Fatalf("unselected provider rejected the effective configuration: %v", err)
	}
	path = writeConfigFixture(t, strings.Replace(sharedConfig, "command: task", "command: task\n    cwd: src", 1),
		"providers: {claude: {enabled: false}}\n")
	if _, err := LoadProjectConfig(path, nil); err != nil {
		t.Fatalf("shared compatibility validation ran before the local override: %v", err)
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
	if err := os.Mkdir(filepath.Join(filepath.Dir(path), LocalConfigFile), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectConfig(path, nil); err == nil || !strings.Contains(err.Error(), "local configuration") {
		t.Fatalf("unreadable local config was ignored: %v", err)
	}
	for _, tt := range []struct{ name, local, want string }{
		{"bad YAML", "providers: [", "failed to parse YAML"},
		{"unknown field", "providers: {codex: {enable: true}}", "field enable"},
		{"wrong value type", "providers: {codex: {enabled: definitely}}", "cannot unmarshal"},
		{"duplicate keys", "env: []\nenv: []", "already defined"},
		{"extra document", "providers: {}\n---\n", "exactly one YAML document"},
		{"unknown provider", "providers: {typo: {enabled: false}}", "unknown provider"},
		{"invalid effective value", "providers: {codex: {approval-policy: invalid}}", "invalid approval-policy"},
		{"clear required field", "context: {template: null}", "context.template is required"},
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

func TestParseProviders(t *testing.T) {
	for _, tt := range []struct {
		value   string
		want    []string
		invalid bool
	}{
		{"", []string{}, false},
		{" \t ", []string{}, false},
		{"codex", []string{"codex"}, false},
		{" codex, gemini ", []string{"codex", "gemini"}, false},
		{"codex,", nil, true},
		{",codex", nil, true},
		{"codex,,claude", nil, true},
		{"codex,codex", nil, true},
		{"Codex", nil, true},
		{"typo", nil, true},
	} {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParseProviders(tt.value)
			if (err != nil) != tt.invalid || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseProviders(%q) = %#v, %v; want %#v, invalid=%v", tt.value, got, err, tt.want, tt.invalid)
			}
		})
	}
}
