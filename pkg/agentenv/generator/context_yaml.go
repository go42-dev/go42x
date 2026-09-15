package generator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// yamlValue emits one inline value using JSON syntax, which is also valid YAML.
// Quoting strings here prevents values from changing types or introducing keys.
func yamlValue(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode YAML value: %w", err)
	}
	// JSON permits a literal NEL character; YAML would normalize it as a newline.
	return strings.ReplaceAll(string(data), "\u0085", `\u0085`), nil
}

// yamlBlockFunc binds yamlBlock to this execution's named templates. A separate
// closure per Process call keeps reusable engines safe for concurrent renders.
func yamlBlockFunc(tmpl *template.Template) func(string, any, ...string) (string, error) {
	rendering := false
	return func(name string, data any, options ...string) (string, error) {
		omitEmpty := false
		for _, option := range options {
			if option != "omit-empty" {
				return "", fmt.Errorf("yamlBlock %q: unknown option %q", name, option)
			}
			omitEmpty = true
		}
		if rendering {
			return "", fmt.Errorf("yamlBlock %q: nested yamlBlock calls are not supported", name)
		}
		rendering = true
		defer func() { rendering = false }()
		return renderYAMLBlock(tmpl, name, data, omitEmpty)
	}
}

func renderYAMLBlock(tmpl *template.Template, name string, data any, omitEmpty bool) (string, error) {
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, name, data); err != nil {
		return "", fmt.Errorf("render YAML template %q: %w", name, err)
	}
	decoder := yaml.NewDecoder(&rendered)
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return "", fmt.Errorf("parse YAML template %q: %w", name, err)
	}
	// Decoding the node also rejects duplicate keys and invalid aliases. Retain
	// the node for encoding, so authored field order and comments are preserved.
	var value any
	if err := document.Decode(&value); err != nil {
		return "", fmt.Errorf("validate YAML template %q: %w", name, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return "", fmt.Errorf("parse trailing YAML in template %q: %w", name, err)
		}
		return "", fmt.Errorf("YAML template %q must contain exactly one document", name)
	}
	if omitEmpty {
		// Expand aliases before pruning so removed anchors cannot leave dangling
		// references. Validation above has already rejected cyclic aliases.
		document = *expandYAMLAliases(&document)
		omitEmptyYAML(&document)
	}
	// Expand inline collections and simplify keys while retaining value quoting and types.
	blockYAMLStyle(&document)
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return "", fmt.Errorf("encode YAML template %q: %w", name, err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("finish YAML template %q: %w", name, err)
	}
	// Bodies and configured values may themselves contain Markdown fences.
	fenceLength, run := 3, 0
	for _, character := range buf.String() {
		if character == '`' {
			run++
			fenceLength = max(fenceLength, run+1)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", fenceLength)
	return fence + "yaml\n" + buf.String() + fence, nil
}

func blockYAMLStyle(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style &^= yaml.FlowStyle
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			// Let the encoder quote keys only when needed. Keep line breaks escaped
			// to prevent YAML folding or newline normalization from changing them.
			if key.Kind == yaml.ScalarNode && !strings.ContainsAny(key.Value, "\r\n\u0085\u2028\u2029") {
				key.Style &^= yaml.SingleQuotedStyle | yaml.DoubleQuotedStyle
			}
		}
	}
	for _, child := range node.Content {
		blockYAMLStyle(child)
	}
}

func expandYAMLAliases(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		return expandYAMLAliases(node.Alias)
	}
	copy := *node
	copy.Anchor = ""
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = expandYAMLAliases(child)
	}
	return &copy
}

// omitEmptyYAML prunes mapping values and sequence items recursively. It reports
// whether a node is empty; document roots remain valid even when entirely empty.
// Only null and the empty string are empty scalars: false, zero, and whitespace stay.
func omitEmptyYAML(node *yaml.Node) bool {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			omitEmptyYAML(child)
		}
	case yaml.ScalarNode:
		return node.Tag == "!!null" || (node.Tag == "!!str" && node.Value == "")
	case yaml.MappingNode:
		kept := node.Content[:0]
		for i := 0; i < len(node.Content); i += 2 {
			if !omitEmptyYAML(node.Content[i+1]) {
				kept = append(kept, node.Content[i], node.Content[i+1])
			}
		}
		node.Content = kept
		return len(kept) == 0
	case yaml.SequenceNode:
		kept := node.Content[:0]
		for _, child := range node.Content {
			if !omitEmptyYAML(child) {
				kept = append(kept, child)
			}
		}
		node.Content = kept
		return len(kept) == 0
	}
	return false
}
