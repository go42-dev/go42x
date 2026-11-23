package assets

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// templates are the bundled files that go42x renders into its output.
//
//go:embed agentenv/project.tpl.gitignore agentenv/go42x.tpl.gitignore
//go:embed agentenv/providers kwb/kwb.tpl.ignore vendoring/.vendor.tpl.yaml
var templates embed.FS

// Paths of the bundled templates.
const (
	ProjectGitIgnore = "agentenv/project.tpl.gitignore"
	DirGitIgnore     = "agentenv/go42x.tpl.gitignore"
	KWBIgnore        = "kwb/kwb.tpl.ignore"
	VendorManifest   = "vendoring/.vendor.tpl.yaml"
)

// ProviderTemplate returns the path of a provider's bundled template.
func ProviderTemplate(provider, name string) string {
	return "agentenv/providers/" + provider + "/" + name
}

// gitIgnoreEscaper quotes the characters that gitignore patterns treat as wildcards.
var gitIgnoreEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)

// TemplateFuncs returns the functions that every go42x template can call.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"gitignore": gitIgnoreEscaper.Replace,
		"indent":    indent,
		"join":      strings.Join,
		"lower":     strings.ToLower,
		"toYAML":    toYAML,
		"trim":      strings.TrimSpace,
		"upper":     strings.ToUpper,
		"yamlValue": YAMLValue,
	}
}

// MissingValue is what a template prints for a key that its data lacks. Renderers reject
// output that contains it; if and with test optional keys without printing anything.
const MissingValue = "<no value>"

// CheckMissingValue reports the first output line that printed a key missing from the
// template data.
func CheckMissingValue(output string) error {
	before, _, found := strings.Cut(output, MissingValue)
	if !found {
		return nil
	}
	return fmt.Errorf("output line %d prints a key that the template context lacks", 1+strings.Count(before, "\n"))
}

// Render executes the bundled template at path with data, the template context of the
// project. Output that prints a missing key is an error.
func Render(path string, data any) (string, error) {
	text, err := templates.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", path, err)
	}
	tmpl, err := template.New(path).Funcs(TemplateFuncs()).Parse(string(text))
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", path, err)
	}

	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render template %s: %w", path, err)
	}
	if err := CheckMissingValue(out.String()); err != nil {
		return "", fmt.Errorf("render template %s: %w", path, err)
	}
	return out.String(), nil
}

// YAMLValue emits one inline value using JSON syntax, which is also valid YAML.
// Quoting strings here prevents values from changing types or introducing keys.
func YAMLValue(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode YAML value: %w", err)
	}
	// JSON permits a literal NEL character; YAML would normalize it as a newline.
	return strings.ReplaceAll(string(data), "\u0085", `\u0085`), nil
}

// toYAML encodes value as block YAML with two-space indentation, without a final newline.
func toYAML(value any) (string, error) {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("encode YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("encode YAML: %w", err)
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// indent prefixes every line of text with spaces.
func indent(spaces int, text string) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(text, "\n", "\n"+prefix)
}
