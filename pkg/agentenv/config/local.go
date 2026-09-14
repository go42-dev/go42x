package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadProjectConfig merges the adjacent optional local file, then applies an
// exact provider selection. Nil preserves configuration choices; an empty slice
// disables every provider. Neither source file is modified.
func LoadProjectConfig(path string, providers []string) (*Config, error) {
	if err := ValidateProviders(providers); err != nil {
		return nil, err
	}
	base, err := readConfigMap(path)
	if err != nil {
		return nil, fmt.Errorf("project configuration: %w", err)
	}
	localPath := filepath.Join(filepath.Dir(path), LocalConfigFile)
	local, err := readConfigMap(localPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("local configuration: %w", err)
	}
	mergeConfigMaps(base, local)
	data, err := yaml.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("encode effective configuration: %w", err)
	}
	cfg, err := decodeConfig(data)
	if err != nil {
		return nil, err
	}
	if providers != nil {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]Provider)
		}
		for name, p := range cfg.Providers {
			p.Enabled = new(false)
			cfg.Providers[name] = p
		}
		for _, name := range providers {
			p := cfg.Providers[name]
			p.Enabled = new(true)
			cfg.Providers[name] = p
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid effective config: %w", err)
	}
	return cfg, nil
}

func readConfigMap(path string) (map[string]any, error) {
	data, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	// Check each source independently, so an override cannot hide a misspelled
	// field, duplicate YAML key, or incorrectly typed value in another source.
	if _, err := decodeConfig(data); err != nil {
		return nil, err
	}
	values := make(map[string]any)
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decode configuration mapping: %w", err)
	}
	if values == nil {
		values = make(map[string]any)
	}
	return values, nil
}

// Maps merge by key; lists and scalar values replace the previous value.
// Explicit null clears a value instead of inheriting it.
func mergeConfigMaps(base, local map[string]any) {
	for key, value := range local {
		baseMap, baseIsMap := base[key].(map[string]any)
		localMap, localIsMap := value.(map[string]any)
		if baseIsMap && localIsMap {
			mergeConfigMaps(baseMap, localMap)
		} else {
			base[key] = value
		}
	}
}

// ParseProviders parses a comma-separated, exact provider selection. An empty
// value explicitly selects no providers, unlike an absent selection (nil).
func ParseProviders(value string) ([]string, error) {
	providers := []string{}
	if strings.TrimSpace(value) != "" {
		for name := range strings.SplitSeq(value, ",") {
			providers = append(providers, strings.TrimSpace(name))
		}
	}
	if err := ValidateProviders(providers); err != nil {
		return nil, err
	}
	return providers, nil
}

// ValidateProviders rejects unknown names, empty entries, and duplicate choices.
func ValidateProviders(providers []string) error {
	seen := make(map[string]bool, len(providers))
	for _, name := range providers {
		switch name {
		case "claude", "codex", "gemini", "crush", "copilot":
		default:
			return fmt.Errorf("unknown provider %q; use claude, codex, gemini, crush, or copilot", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate provider %q", name)
		}
		seen[name] = true
	}
	return nil
}
