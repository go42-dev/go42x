package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// VariableProjectRoot names the absolute project directory during configuration loading.
const VariableProjectRoot = "GO42X_PROJECT_ROOT"

// builtInReferencePattern preserves native GitHub expressions before matching built-ins.
var builtInReferencePattern = regexp.MustCompile(
	`(?s)\$\{\{(?:'[^']*'|[^'])*?\}\}|\$\{GO42X_[A-Za-z0-9_]*\}?|\$GO42X_[A-Za-z0-9_]*`,
)

// Variables supplies generation-time values for the reserved GO42X_ namespace.
// Values are substituted once without reading environment variables.
type Variables map[string]string

// VariablesForProject returns built-ins for the supplied project directory.
func VariablesForProject(root string) (Variables, error) {
	if len(strings.TrimSpace(root)) == 0 {
		return nil, fmt.Errorf("project root is required for configuration variables")
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	return Variables{VariableProjectRoot: absolute}, nil
}

func variablesForConfigFile(path string) (Variables, error) {
	root := filepath.Dir(path)
	if filepath.Base(root) == Directory {
		root = filepath.Dir(root)
	}

	return VariablesForProject(root)
}

func resolveConfig(values map[string]any, variables Variables) (*Config, error) {
	resolved, err := resolveConfigMap(values, variables, "")
	if err != nil {
		return nil, fmt.Errorf("resolve configuration variables: %w", err)
	}

	data, err := yaml.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encode effective configuration: %w", err)
	}

	return decodeConfig(data)
}

func resolveConfigMap[K comparable](values map[K]any, variables Variables, fieldPath string) (map[K]any, error) {
	if values == nil {
		return nil, nil
	}

	keys := slices.Collect(maps.Keys(values))
	slices.SortFunc(keys, func(left, right K) int {
		return strings.Compare(fmt.Sprint(left), fmt.Sprint(right))
	})
	resolved := make(map[K]any, len(values))
	for _, key := range keys {
		path := fmt.Sprint(key)
		if len(fieldPath) > 0 {
			path = fieldPath + "." + path
		}

		value, err := resolveConfigValue(values[key], variables, path)
		if err != nil {
			return nil, err
		}

		resolved[key] = value
	}

	return resolved, nil
}

func resolveConfigValue(value any, variables Variables, fieldPath string) (any, error) {
	switch value := value.(type) {
	case string:
		resolved, err := variables.resolveString(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fieldPath, err)
		}

		return resolved, nil
	case map[string]any:
		return resolveConfigMap(value, variables, fieldPath)
	case map[any]any:
		return resolveConfigMap(value, variables, fieldPath)
	case []any:
		if value == nil {
			return value, nil
		}

		resolved := make([]any, len(value))
		for i, item := range value {
			item, err := resolveConfigValue(item, variables, fmt.Sprintf("%s[%d]", fieldPath, i))
			if err != nil {
				return nil, err
			}

			resolved[i] = item
		}

		return resolved, nil
	default:
		return value, nil
	}
}

func (v Variables) resolveString(value string) (string, error) {
	var resolutionErr error
	resolved := builtInReferencePattern.ReplaceAllStringFunc(value, func(reference string) string {
		if strings.HasPrefix(reference, "${{") || resolutionErr != nil {
			return reference
		}

		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(reference, "$"), "{"), "}")
		if !strings.HasPrefix(reference, "${") || !strings.HasSuffix(reference, "}") {
			resolutionErr = fmt.Errorf("invalid built-in reference %q; use ${NAME}", name)
			return reference
		}

		binding, exists := v[name]
		if !exists {
			resolutionErr = fmt.Errorf("unknown built-in variable %q", name)
			return reference
		}

		return binding
	})
	if resolutionErr != nil {
		return "", resolutionErr
	}

	return resolved, nil
}
