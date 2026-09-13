package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(Schema()))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("go42x.schema.json", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("go42x.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestSchemaRejectsRemovedContextDirectories(t *testing.T) {
	compiled := compileSchema(t)
	for _, field := range []string{"modes-dir", "workflows-dir"} {
		t.Run(field, func(t *testing.T) {
			data := []byte("version: '1.0'\nproject: {name: example}\n" +
				"context: {template: agents.tpl.md, " + field + ": old/}\nproviders: {codex: {}}\n")
			var document map[string]any
			if err := yaml.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(document); err == nil || !strings.Contains(err.Error(), field) {
				t.Errorf("schema validation = %v, want rejection of %s", err, field)
			}
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

func TestSchemaMatchesConfigValidation(t *testing.T) {
	compiled := compileSchema(t)
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
		{"unsupported provider option", "providers: {copilot: {approval-policy: never}}", true},
		{"invalid approval policy", "providers: {codex: {approval-policy: invalid}}", true},
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
			var document map[string]any
			if err := yaml.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(document); (err != nil) != tt.wantErr {
				t.Errorf("schema validation = %v, want error %v", err, tt.wantErr)
			}
			path := filepath.Join(t.TempDir(), "go42x.yaml")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
