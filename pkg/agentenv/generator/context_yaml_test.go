package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

func defaultContextTemplate(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", "agentenv", "template", "agents.tpl.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func renderDefaultContext(t *testing.T, data map[string]any) (string, error) {
	t.Helper()
	engine := newTemplateEngine("")
	return engine.Process(engine.InjectChunks(defaultContextTemplate(t), ""), data)
}

func decodeContextBlock(t *testing.T, content string) (map[string]any, string) {
	t.Helper()
	start := strings.Index(content, "```")
	if start < 0 {
		t.Fatal("generated context has no fenced block")
	}
	opening, rest, ok := strings.Cut(content[start:], "\n")
	if !ok || !strings.HasSuffix(opening, "yaml") {
		t.Fatalf("expected a YAML fence, got %q", opening)
	}
	fence := strings.TrimSuffix(opening, "yaml")
	body, after, ok := strings.Cut(rest, "\n"+fence)
	if !ok || (after != "" && !strings.HasPrefix(after, "\n")) {
		t.Fatal("YAML fence is not closed on its own line")
	}
	var data map[string]any
	// The delimiter consumes the final YAML newline; keep it for literal scalars.
	if err := yaml.Unmarshal([]byte(body+"\n"), &data); err != nil {
		t.Fatalf("invalid generated context YAML: %v", err)
	}
	return data, after
}

func TestContextYAMLRoundTrip(t *testing.T) {
	description := "Service: # quoted \"text\"\n```go\nexample()\n```\n````\n{{ .not_a_template }}"
	request := "Fix this:\n```yaml\nversion: 1.27\n```\nKeep: \"yes\""
	data := map[string]any{
		"project": map[string]any{
			"name": "go42x", "description": description, "language": "go", "tags": []string{"go42x", "cli"},
			"metadata": map[string]string{"number": "1.27", "boolean": "true", "null": "null"},
		},
		"git": map[string]any{
			"root": "/project", "remote": "git@example.com:org/repo.git",
			"branch": "unpublished-branch", "commit": "unpublished-commit", "tag": "unpublished-tag",
		},
		"environment": map[string]any{
			"os": "darwin", "arch": "arm64", "is_ci": false, "ci_mode": "", "working_dir": "/project",
			"variables": map[string]string{"CUSTOM": "false\nvalue: yes"},
		},
		"golang": map[string]any{
			"go_version": "1.27",
			"env": map[string]string{
				"GOVERSION":   "go1.27.9",
				"GOTOOLCHAIN": "auto",
				"CGO_ENABLED": "0",
				"GOFLAGS":     "-tags=example",
			},
		},
		"github_actions": map[string]any{
			"ref_name": "main", "user_request": request,
			"runner": map[string]any{"name": "unpublished-runner"},
		},
		"mcp": map[string]config.MCPServer{
			"enabled": {
				Enabled: true, Type: "http", URL: "https://unpublished-host.example",
				Headers: map[string]string{"Authorization": "unpublished-token"},
				Env:     map[string]string{"TOKEN": "unpublished-environment"}, Command: "unpublished-command",
			},
			"disabled": {Enabled: false, Type: "stdio"},
		},
	}
	content, err := renderDefaultContext(t, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "\n`````yaml\n") {
		t.Fatal("context fence must be longer than fences in its values")
	}
	decoded, _ := decodeContextBlock(t, content)
	project := decoded["project"].(map[string]any)
	if project["description"] != description ||
		!reflect.DeepEqual(project["tags"], []any{"go42x", "cli"}) {
		t.Fatalf("project values did not round-trip: %+v", project)
	}
	metadata := project["metadata"].(map[string]any)
	for key, want := range map[string]string{"number": "1.27", "boolean": "true", "null": "null"} {
		if metadata[key] != want {
			t.Errorf("metadata %s = %#v; want string %q", key, metadata[key], want)
		}
	}
	environment := decoded["environment"].(map[string]any)
	if environment["is_ci"] != false || environment["go_version"] != nil ||
		environment["variables"].(map[string]any)["CUSTOM"] != "false\nvalue: yes" {
		t.Fatalf("environment values did not round-trip: %+v", environment)
	}
	golang := decoded["golang"].(map[string]any)
	if golang["go_version"] != "1.27" {
		t.Fatalf("Go requirement did not round-trip: %+v", golang)
	}
	for key, want := range data["golang"].(map[string]any)["env"].(map[string]string) {
		if got := golang["env"].(map[string]any)[key]; got != want {
			t.Errorf("Go env %s = %#v, want string %q", key, got, want)
		}
		if !strings.Contains(content, "\n    "+key+": "+strconv.Quote(want)+"\n") {
			t.Errorf("Go env %s should have an unquoted key and quoted value", key)
		}
	}
	github := decoded["github_actions"].(map[string]any)
	if github["ref"] != "main" || github["user_request"] != request {
		t.Fatalf("GitHub context did not round-trip: %+v", github)
	}
	wantMCP := map[string]any{
		"tools":   "all_exposed",
		"servers": map[string]any{"enabled": map[string]any{"transport": "http"}},
	}
	if !reflect.DeepEqual(decoded["mcp"], wantMCP) {
		t.Fatalf("MCP context = %+v, want %+v", decoded["mcp"], wantMCP)
	}
	if strings.Contains(content, "unpublished-") {
		t.Fatal("context exposed fields outside the published context")
	}
	wantRepository := map[string]any{"root": "/project", "remote": "git@example.com:org/repo.git"}
	if !reflect.DeepEqual(decoded["repository"], wantRepository) {
		t.Fatalf("repository context = %+v, want %+v", decoded["repository"], wantRepository)
	}
	again, err := renderDefaultContext(t, data)
	if err != nil || content != again {
		t.Fatalf("context rendering is not deterministic: %v", err)
	}
	if data["git"].(map[string]any)["branch"] != "unpublished-branch" {
		t.Fatal("context rendering modified collected values used by custom templates")
	}
}

func TestContextYAMLOmitsUnavailableSections(t *testing.T) {
	content, err := renderDefaultContext(t, map[string]any{
		"project": map[string]any{"name": "example"},
		"mcp":     map[string]config.MCPServer{"disabled": {Enabled: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := decodeContextBlock(t, content)
	want := map[string]any{"project": map[string]any{"name": "example"}}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("context = %+v; want %+v", data, want)
	}
}

func TestDefaultContextKeepsMarkdownChunks(t *testing.T) {
	template := defaultContextTemplate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agents.tpl.md"), []byte(template), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "chunks"), 0700); err != nil {
		t.Fatal(err)
	}
	chunk := "### Rules\n\n- Work on {{ .project.name }}.\n\n```sh\ngo test ./...\n```"
	if err := os.WriteFile(filepath.Join(dir, "chunks", "rules.tpl.md"), []byte(chunk), 0600); err != nil {
		t.Fatal(err)
	}
	content, err := newTemplateEngine(dir).Render(
		config.Context{Template: "agents.tpl.md", ChunksDir: "chunks"},
		map[string]any{"project": map[string]any{"name": "example"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	data, after := decodeContextBlock(t, content)
	if data["project"].(map[string]any)["name"] != "example" {
		t.Fatal("missing YAML context")
	}
	want := "\n\n## Instructions\n\n" + strings.ReplaceAll(chunk, "{{ .project.name }}", "example") + "\n"
	if after != want {
		t.Fatalf("Markdown chunks changed: got %q, want %q", after, want)
	}
}

func TestYAMLTemplateControlsStructure(t *testing.T) {
	engine := newTemplateEngine("")
	source := `{{ define "facts" }}
# Custom fields, in the author's order.
z_service:
  renamed: {{ yamlValue .project.name }}
  revision: {{ yamlValue .git.commit }}
a_settings: {{ yamlValue .settings }}
{{ end }}
{{ yamlBlock "facts" . }}

{{ define "part" }}selected: {{ yamlValue .name }}{{ end }}
{{ yamlBlock "part" .project }}`
	data := map[string]any{
		"project":  map[string]any{"name": "custom", "description": "omitted"},
		"git":      map[string]any{"commit": "abc123"},
		"settings": map[string]any{"enabled": false, "version": "1.27"},
	}
	content, err := engine.Process(source, data)
	if err != nil {
		t.Fatal(err)
	}
	decoded, after := decodeContextBlock(t, content)
	want := map[string]any{
		"z_service":  map[string]any{"renamed": "custom", "revision": "abc123"},
		"a_settings": data["settings"],
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("custom structure = %#v; want %#v", decoded, want)
	}
	if strings.Index(content, "z_service:") > strings.Index(content, "a_settings:") ||
		!strings.Contains(content, "# Custom fields, in the author's order.") {
		t.Fatal("authored field order or comments were lost")
	}
	selected, _ := decodeContextBlock(t, after)
	if !reflect.DeepEqual(selected, map[string]any{"selected": "custom"}) {
		t.Fatalf("second YAML block did not receive its selected data: %#v", selected)
	}
}

func TestYAMLValueRoundTrip(t *testing.T) {
	for _, value := range []any{
		nil, false, 0, 1.5, "", "\n", "trailing\n\n", "true", "null", "1.27", "2026-09-15",
		"\n\n", " \n", "\t\n", "\r\n", "\nleading", "\n\nleading", "\x00\x01",
		"first\u0085second", "first\u2028second\u2029third", "Ελληνικά 日本語 🚀",
		"quotes: \"yes\"\n---\ninjected: true\n{{ .not_a_template }}\n",
		[]any{"one", "false", 2},
		map[string]any{"key:\nquoted": "value", "empty": nil},
		map[string]any{
			"CGO_ENABLED": "1", "true": "false", "null": "null", "1": "0", "2026-09-15": "date",
			"colon: key": "value", "hash # key": "value", "": "empty key", " first ": "spaces",
			"line\nbreak": "LF", "line\rbreak": "CR", "line\u0085break": "NEL",
			"line\u2028break": "LS", "line\u2029break": "PS", "\x00": "NUL",
		},
	} {
		t.Run(fmt.Sprintf("%T/%v", value, value), func(t *testing.T) {
			content, err := newTemplateEngine("").Process(
				`{{ define "value" }}value: {{ yamlValue .value }}{{ end }}{{ yamlBlock "value" . }}`,
				map[string]any{"value": value},
			)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := decodeContextBlock(t, content)
			if len(data) != 1 || !reflect.DeepEqual(data["value"], value) {
				t.Fatalf("value = %#v; want %#v; rendered %q", data, value, content)
			}
		})
	}
}

func TestYAMLBlockErrors(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"syntax", "value: [", "parse YAML template"},
		{"duplicate keys", "value: a\nvalue: b\n", "validate YAML template"},
		{"multiple documents", "value: a\n---\nvalue: b\n", "exactly one document"},
		{"malformed trailing document", "value: a\n---\nvalue: [", "parse trailing YAML"},
		{"empty", "", "parse YAML template"},
		{"recursive", `{{ yamlBlock "context" . }}`, "nested yamlBlock"},
		{"unsupported value", `value: {{ yamlValue .unsupported }}`, "encode YAML value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := `{{ define "context" }}` + tt.body + `{{ end }}partial{{ yamlBlock "context" . }}`
			result, err := newTemplateEngine("").Process(source, map[string]any{"unsupported": make(chan int)})
			if err == nil || !strings.Contains(err.Error(), tt.want) || result != "" {
				t.Fatalf("Process() = %q, %v; want no output and %q", result, err, tt.want)
			}
		})
	}
	if _, err := newTemplateEngine("").Process(`{{ yamlBlock "missing" . }}`, nil); err == nil ||
		!strings.Contains(err.Error(), `render YAML template "missing"`) {
		t.Fatalf("missing named template = %v", err)
	}
}

func TestYAMLTemplateConcurrentRenders(t *testing.T) {
	engine := newTemplateEngine("")
	for i := range 8 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			source := `{{ define "context" }}value: {{ yamlValue .value }}{{ end }}{{ yamlBlock "context" . }}`
			content, err := engine.Process(source, map[string]any{"value": i})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := decodeContextBlock(t, content)
			if data["value"] != i {
				t.Fatalf("render received another call's context: %#v", data)
			}
		})
	}
}

func TestYAMLBlockOmitEmpty(t *testing.T) {
	source := `{{ define "context" }}
# Keep this order.
z_flags:
  enabled: false
  retries: 0
  optional:
empty_string: ""
empty_map: {}
empty_list: []
empty_section:
  child:
    value: null
a_text:
  space: " "
  newline: "\n"
  null_string: "null"
  zero_string: "0"
list: [null, "", [], {}, {missing: null}, false, 0, {drop: "", keep: 0}]
{{ end }}
{{ yamlBlock "context" . "omit-empty" }}

{{ yamlBlock "context" . }}`
	content, err := newTemplateEngine("").Process(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, after := decodeContextBlock(t, content)
	want := map[string]any{
		"z_flags": map[string]any{"enabled": false, "retries": 0},
		"a_text":  map[string]any{"space": " ", "newline": "\n", "null_string": "null", "zero_string": "0"},
		"list":    []any{false, 0, map[string]any{"keep": 0}},
	}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("omitted context = %#v; want %#v", data, want)
	}
	if !strings.Contains(content, "# Keep this order.") ||
		strings.Index(content, "z_flags:") > strings.Index(content, "a_text:") {
		t.Fatal("omission changed retained comments or field order")
	}
	unchanged, _ := decodeContextBlock(t, after)
	for _, key := range []string{"empty_string", "empty_map", "empty_list", "empty_section"} {
		if _, ok := unchanged[key]; !ok {
			t.Errorf("omission leaked into a block without the option: %s", key)
		}
	}
}

func TestYAMLBlockOmitEmptyAliases(t *testing.T) {
	source := `{{ define "context" }}
empty: &empty {value: null}
empty_alias: *empty
defaults: &defaults {enabled: false, optional: ""}
copy: *defaults
? &key key
: null
copied_key: *key
{{ end }}
{{ yamlBlock "context" . "omit-empty" }}`
	content, err := newTemplateEngine("").Process(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := decodeContextBlock(t, content)
	want := map[string]any{
		"defaults":   map[string]any{"enabled": false},
		"copy":       map[string]any{"enabled": false},
		"copied_key": "key",
	}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("omission broke YAML aliases: %#v; want %#v", data, want)
	}
}

func TestYAMLBlockOmitEmptyRoot(t *testing.T) {
	for _, body := range []string{"{}", "empty: {nested: null}\n", "empty: [null, {}, \"\"]\n"} {
		source := `{{ define "context" }}` + body + `{{ end }}{{ yamlBlock "context" . "omit-empty" }}`
		content, err := newTemplateEngine("").Process(source, nil)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := decodeContextBlock(t, content)
		if data == nil || len(data) != 0 {
			t.Fatalf("empty root must remain a valid empty mapping: %#v", data)
		}
	}
}

func TestYAMLBlockOmitEmptyErrors(t *testing.T) {
	for _, tc := range []struct{ body, option, want string }{
		{"value: null", "omit-emtpy", "unknown option"},
		{"value: null\nvalue: null\n", "omit-empty", "validate YAML template"},
		{"value: &cycle [*cycle]", "omit-empty", "validate YAML template"},
	} {
		source := `{{ define "context" }}` + tc.body + `{{ end }}{{ yamlBlock "context" . ` + strconv.Quote(
			tc.option,
		) + ` }}`
		content, err := newTemplateEngine("").Process(source, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) || content != "" {
			t.Fatalf("Process() = %q, %v; want no output and %q", content, err, tc.want)
		}
	}
}
