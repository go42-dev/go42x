package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVariablesResolveString(t *testing.T) {
	t.Setenv(VariableProjectRoot, "must-not-use-process-environment")
	variables := Variables{
		VariableProjectRoot: "/work/project with spaces/${GO42X_OTHER}",
		"GO42X_NAME":        "example",
	}
	for _, tt := range []struct {
		name  string
		input string
		want  string
		err   string
	}{
		{
			name:  "literal",
			input: "unchanged",
			want:  "unchanged",
		},
		{
			name:  "multiple and embedded",
			input: "${GO42X_PROJECT_ROOT}/${GO42X_NAME}/${GO42X_NAME}.yaml",
			want:  "/work/project with spaces/${GO42X_OTHER}/example/example.yaml",
		},
		{
			name:  "runtime environment",
			input: "Bearer ${TOKEN}, $TOKEN, ${TOKEN:-fallback}",
			want:  "Bearer ${TOKEN}, $TOKEN, ${TOKEN:-fallback}",
		},
		{
			name:  "workflow expression",
			input: "${{ format('}} ${GO42X_UNKNOWN}', github.workspace) }}:${GO42X_NAME}",
			want:  "${{ format('}} ${GO42X_UNKNOWN}', github.workspace) }}:example",
		},
		{
			name:  "unknown built-in",
			input: "secret-prefix ${GO42X_UNKNOWN} secret-suffix",
			err:   "unknown built-in variable \"GO42X_UNKNOWN\"",
		},
		{
			name:  "unbraced built-in",
			input: "$GO42X_PROJECT_ROOT",
			err:   "invalid built-in reference \"GO42X_PROJECT_ROOT\"",
		},
		{
			name:  "unclosed reference",
			input: "secret-prefix ${GO42X_PROJECT_ROOT",
			err:   "invalid built-in reference \"GO42X_PROJECT_ROOT\"",
		},
		{
			name:  "unsupported fallback",
			input: "${GO42X_PROJECT_ROOT:-secret-fallback}",
			err:   "invalid built-in reference \"GO42X_PROJECT_ROOT\"",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := variables.resolveString(tt.input)

			if len(tt.err) > 0 {
				if err == nil || !strings.Contains(err.Error(), tt.err) || strings.Contains(err.Error(), "secret-") {
					t.Fatalf("resolveString() = %q, %v; want a redacted error containing %q", got, err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("resolveString() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestResolveConfigMapPreservesKeysTypesAndInput(t *testing.T) {
	values := map[string]any{
		"${GO42X_KEY}": "${GO42X_PROJECT_ROOT}",
		"nested":       []any{true, 42, 1.5, nil, map[any]any{12: "${GO42X_PROJECT_ROOT}/file"}},
	}
	wantOriginal := map[string]any{
		"${GO42X_KEY}": "${GO42X_PROJECT_ROOT}",
		"nested":       []any{true, 42, 1.5, nil, map[any]any{12: "${GO42X_PROJECT_ROOT}/file"}},
	}
	want := map[string]any{
		"${GO42X_KEY}": "/project",
		"nested":       []any{true, 42, 1.5, nil, map[any]any{12: "/project/file"}},
	}

	got, err := resolveConfigMap(values, Variables{VariableProjectRoot: "/project"}, "")

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved configuration = %#v, %v; want %#v", got, err, want)
	}
	if !reflect.DeepEqual(values, wantOriginal) {
		t.Fatalf("resolution modified its input: %#v", values)
	}
}

func TestVariablesForProject(t *testing.T) {
	t.Chdir(t.TempDir())
	variables, err := VariablesForProject(filepath.Join("checkout", "..", "project"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("project")
	if err != nil {
		t.Fatal(err)
	}

	if variables[VariableProjectRoot] != want {
		t.Fatalf("project root = %q, want %q", variables[VariableProjectRoot], want)
	}
	if _, err := VariablesForProject(""); err == nil {
		t.Fatal("empty project root must be rejected")
	}
}

func TestProjectConfigResolvesAllStringValuesBeforeValidation(t *testing.T) {
	const base = `version: '${GO42X_VERSION}'
project:
  name: '${GO42X_NAME}'
  description: '${GO42X_PROJECT_ROOT}'
  tags: ['${GO42X_NAME}']
  metadata: {'${GO42X_KEY}': '${GO42X_PROJECT_ROOT}'}
context:
  template: '${GO42X_PROJECT_ROOT}/.go42x/agents.tpl.md'
  temp-dir: '${GO42X_PROJECT_ROOT}/.build'
providers:
  codex: {approval-policy: '${GO42X_APPROVAL}'}
  gh-aw:
    enabled: false
    bindings:
      GENERATION_ROOT: '${GO42X_PROJECT_ROOT}'
      PROJECT_DIR: '${{ github.workspace }}'
mcp:
  local:
    enabled: true
    name: local
    command: '${GO42X_PROJECT_ROOT}/bin/server'
    args: ['--root=${GO42X_PROJECT_ROOT}']
    cwd: '${GO42X_PROJECT_ROOT}'
    env: {DATA: '${GO42X_PROJECT_ROOT}/data', TOKEN: '${TOKEN}'}
    container:
      image: example/tools
      command: server
      mounts: ['${GO42X_PROJECT_ROOT}:/workspace:rw']
  remote:
    name: remote
    type: http
    url: https://example.com/mcp
    headers:
      IJ_MCP_SERVER_PROJECT_PATH: '${GO42X_PROJECT_ROOT}'
      Authorization: 'Bearer ${TOKEN}'
`
	variables := Variables{
		VariableProjectRoot: "/work/project",
		"GO42X_VERSION":     SupportedVersion,
		"GO42X_NAME":        "example",
		"GO42X_APPROVAL":    "never",
	}

	cfg, err := ParseProjectConfig([]byte(base), nil, nil, variables)

	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != SupportedVersion || cfg.Project.Name != "example" || cfg.Project.Tags[0] != "example" ||
		cfg.Project.Description != "/work/project" || cfg.Project.Metadata["${GO42X_KEY}"] != "/work/project" ||
		cfg.Context.Template != "/work/project/.go42x/agents.tpl.md" || cfg.Context.TempDir != "/work/project/.build" ||
		*cfg.Providers["codex"].ApprovalPolicy != "never" {
		t.Fatalf("project, context, or provider values were not resolved: %+v", cfg)
	}
	bindings := cfg.Providers["gh-aw"].Bindings
	if bindings["GENERATION_ROOT"] != "/work/project" || bindings["PROJECT_DIR"] != "${{ github.workspace }}" {
		t.Fatalf("unexpected workflow bindings: %+v", bindings)
	}
	local := cfg.MCP["local"]
	if local.Command != "/work/project/bin/server" || local.Args[0] != "--root=/work/project" ||
		local.CWD != "/work/project" || local.Env["DATA"] != "/work/project/data" || local.Env["TOKEN"] != "${TOKEN}" ||
		local.Container.Mounts[0] != "/work/project:/workspace:rw" {
		t.Fatalf("local MCP values were not resolved: %+v", local)
	}
	remote := cfg.MCP["remote"]
	if remote.Headers["IJ_MCP_SERVER_PROJECT_PATH"] != "/work/project" ||
		remote.Headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Fatalf("unexpected remote headers: %+v", remote.Headers)
	}
}

func TestProjectConfigResolvesAfterOverridesAndReportsField(t *testing.T) {
	base := strings.Replace(sharedConfig, "name: shared", "name: '${GO42X_REMOVED}'", 1)
	variables := Variables{VariableProjectRoot: "/work/project"}
	cfg, err := ParseProjectConfig([]byte(base), []byte("project: {name: overridden}"), nil, variables)
	if err != nil || cfg.Project.Name != "overridden" {
		t.Fatalf("overridden unresolved value = %+v, %v", cfg, err)
	}

	for _, tt := range []struct {
		name  string
		local string
		field string
	}{
		{
			name:  "list",
			local: "project: {tags: [safe, '${GO42X_UNKNOWN}']}",
			field: "project.tags[1]",
		},
		{
			name:  "binding",
			local: "providers: {gh-aw: {bindings: {ROOT: '${GO42X_UNKNOWN}'}}}",
			field: "providers.gh-aw.bindings.ROOT",
		},
		{
			name:  "header",
			local: "mcp: {server: {headers: {Authorization: 'secret-token ${GO42X_UNKNOWN}'}}}",
			field: "mcp.server.headers.Authorization",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProjectConfig([]byte(sharedConfig), []byte(tt.local), nil, variables)

			if err == nil || !strings.Contains(err.Error(), tt.field) ||
				!strings.Contains(err.Error(), "GO42X_UNKNOWN") || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("ParseProjectConfig() = %v; want redacted error for %s", err, tt.field)
			}
		})
	}
}

func TestConfigLoadersResolveRootFromConfigLocation(t *testing.T) {
	t.Setenv(VariableProjectRoot, "must-not-use-environment")
	for _, directory := range []string{".", Directory} {
		t.Run(directory, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, directory)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			base := strings.Replace(sharedConfig, "name: shared", "name: '${GO42X_PROJECT_ROOT}'", 1)
			path := filepath.Join(dir, FileProject)
			if err := os.WriteFile(path, []byte(base), 0600); err != nil {
				t.Fatal(err)
			}
			local := []byte("context: {temp-dir: '${GO42X_PROJECT_ROOT}/scratch'}")
			localPath := filepath.Join(dir, FileLocal)
			if err := os.WriteFile(localPath, local, 0600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(t.TempDir())

			single, err := LoadConfig(path)
			if err != nil || single.Project.Name != root {
				t.Fatalf("LoadConfig() = %+v, %v; want project root %q", single, err, root)
			}
			merged, err := LoadProjectConfig(path, nil)
			if err != nil || merged.Project.Name != root || merged.Context.TempDir != root+"/scratch" {
				t.Fatalf("LoadProjectConfig() = %+v, %v; want root %q and overridden temp directory", merged, err, root)
			}
			for path, want := range map[string][]byte{path: []byte(base), localPath: local} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("configuration source changed: %s, %v", path, err)
				}
			}
		})
	}
}
