package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/assets"
)

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "schemas", "go42x.schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("go42x.schema.yaml", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("go42x.schema.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestSchemaDefaultConfig(t *testing.T) {
	templates, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(templates, "go42x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := compileSchema(t).Validate(document); err != nil {
		t.Fatalf("bundled configuration does not match the editor schema: %v", err)
	}
}

func TestSchemaRepresentativeFields(t *testing.T) {
	compiled := compileSchema(t)
	const base = `version: "1.0"
project: {name: example}
context: {template: agents.tpl.md}
`
	for _, tt := range []struct {
		name    string
		content string
		valid   bool
	}{
		{
			"disabled provider",
			"providers: {codex: {enabled: false}}",
			true,
		},
		{
			"workflow provider",
			"providers: {gh-aw: {enabled: true}}",
			true,
		},
		{
			name:    "workflow bindings",
			content: "providers: {gh-aw: {bindings: {TOKEN: '${{ github.token }}'}}}",
			valid:   true,
		},
		{
			name:    "empty workflow bindings",
			content: "providers: {gh-aw: {bindings: {}}}",
			valid:   true,
		},
		{
			name:    "invalid binding name",
			content: "providers: {gh-aw: {bindings: {bad-name: value}}}",
		},
		{
			name:    "empty binding name",
			content: "providers: {gh-aw: {bindings: {'': value}}}",
		},
		{
			name:    "blank binding",
			content: "providers: {gh-aw: {bindings: {TOKEN: ' '}}}",
		},
		{
			name:    "binding must be a string",
			content: "providers: {gh-aw: {bindings: {TOKEN: []}}}",
		},
		{
			name:    "bindings on another provider",
			content: "providers: {codex: {bindings: {TOKEN: value}}}",
		},
		{
			"workflow provider cannot select engine",
			"providers: {gh-aw: {engine: codex}}",
			false,
		},
		{
			"provider enabled must be boolean",
			"providers: {codex: {enabled: 'false'}}",
			false,
		},
		{
			"HTTP server",
			`providers: {codex: {}}
mcp: {remote: {name: remote, type: http, url: https://example.com/mcp}}`,
			true,
		},
		{
			name: "built-in working directory",
			content: `providers: {codex: {}}
mcp: {local: {name: local, command: task, cwd: '${GO42X_PROJECT_ROOT}/src'}}`,
			valid: true,
		},
		{
			"MCP container",
			`providers: {codex: {}}
mcp: {local: {name: local, command: task, container: {image: example/tools, command: go42x}}}`,
			true,
		},
		{
			"incomplete MCP container",
			`providers: {gh-aw: {}}
mcp: {local: {name: local, command: task, container: {image: example/tools}}}`,
			false,
		},
		{
			"HTTP container",
			`providers: {gh-aw: {}}
mcp: {remote: {name: remote, type: http, url: https://example.com/mcp, container: {image: example/tools, command: go42x}}}`,
			false,
		},
		{
			"provider containers removed",
			`providers: {gh-aw: {containers: {local: {image: example/tools, command: go42x}}}}`,
			false,
		},
		{
			"tools must be an array",
			`providers: {codex: {}}
mcp: {local: {name: local, command: example, tools: search}}`,
			false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var document map[string]any
			if err := yaml.Unmarshal([]byte(base+tt.content), &document); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(document); (err == nil) != tt.valid {
				t.Errorf("schema validation = %v, want valid %v", err, tt.valid)
			}
		})
	}
}
