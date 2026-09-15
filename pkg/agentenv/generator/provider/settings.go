package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

// prepareJSONFile replaces the supplied top-level fields, preserving other settings.
func (p *BaseProvider) prepareJSONFile(plan *output.Plan, path string, data any) error {
	return p.prepareJSONSettings(plan, path, data, nil, nil)
}

// prepareJSONSettings replaces managed fields and fills missing defaults. Paths name
// individual fields so replacing a tool list never replaces its parent object.
// A missing managed field in data deletes that field from the existing settings.
func (p *BaseProvider) prepareJSONSettings(
	plan *output.Plan,
	path string,
	data any,
	managed, defaults []string,
) error {
	content, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to encode settings: %w", err)
	}
	generated, err := decodeJSONSettings(content)
	if err != nil {
		return fmt.Errorf("invalid generated settings: %w", err)
	}
	previous, exists, err := plan.Read(path)
	if err != nil {
		return err
	}
	settings := make(map[string]any)
	if exists {
		settings, err = decodeJSONSettings(previous)
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
	}
	before, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to encode existing settings: %w", err)
	}
	if managed == nil {
		maps.Copy(settings, generated)
	}
	for _, field := range managed {
		value, present := jsonSetting(generated, field)
		if err := setJSONSetting(settings, field, value, present, false); err != nil {
			return fmt.Errorf("failed to update %s: %w", path, err)
		}
	}
	for _, field := range defaults {
		value, present := jsonSetting(generated, field)
		if !present {
			continue
		}
		if err := setJSONSetting(settings, field, value, true, true); err != nil {
			return fmt.Errorf("failed to update %s: %w", path, err)
		}
	}
	after, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to encode settings: %w", err)
	}
	if exists && bytes.Equal(before, after) {
		return plan.Write(path, previous, output.Settings, false)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, after, "", "  "); err != nil {
		return fmt.Errorf("failed to format settings: %w", err)
	}
	return plan.Write(path, formatted.Bytes(), output.Settings, false)
}

func decodeJSONSettings(content []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	// Unknown settings may contain integers too large to round-trip as float64.
	decoder.UseNumber()
	var settings map[string]any
	if err := decoder.Decode(&settings); err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, fmt.Errorf("settings must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("settings must contain a single JSON object")
	}
	return settings, nil
}

func jsonSetting(settings map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	for i, part := range parts {
		value, present := settings[part]
		if !present || i == len(parts)-1 {
			return value, present
		}
		var ok bool
		settings, ok = value.(map[string]any)
		if !ok {
			return nil, false
		}
	}
	return nil, false
}

func setJSONSetting(settings map[string]any, path string, value any, present, onlyMissing bool) error {
	parts := strings.Split(path, ".")
	for i, part := range parts[:len(parts)-1] {
		existing, found := settings[part]
		if !found {
			if !present {
				return nil
			}
			child := make(map[string]any)
			settings[part] = child
			settings = child
			continue
		}
		child, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("setting %s must be an object", strings.Join(parts[:i+1], "."))
		}
		settings = child
	}
	key := parts[len(parts)-1]
	if _, found := settings[key]; onlyMissing && found {
		return nil
	}
	if present {
		settings[key] = value
	} else {
		delete(settings, key)
	}
	return nil
}
