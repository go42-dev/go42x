package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
)

// LoadOption configures project configuration loading.
type LoadOption func(*loadOptions)

type loadOptions struct {
	localOverrides bool
}

// WithLocalOverrides controls whether the adjacent local configuration is read.
// Local overrides are enabled by default.
func WithLocalOverrides(enabled bool) LoadOption {
	return func(options *loadOptions) {
		options.localOverrides = enabled
	}
}

// LoadProjectConfig optionally merges the adjacent local file, resolves built-ins,
// applies an exact provider selection, and validates the effective configuration.
// Nil providers preserve configuration choices; an empty slice disables every
// provider. Neither source file is modified.
func LoadProjectConfig(path string, providers []string, opts ...LoadOption) (*Config, error) {
	options := loadOptions{localOverrides: true}
	for _, opt := range opts {
		opt(&options)
	}

	if err := provider.ValidateProviders(providers, ProviderNames()); err != nil {
		return nil, err
	}

	base, err := readConfig(path)
	if err != nil {
		return nil, fmt.Errorf("project configuration: %w", err)
	}

	var local []byte
	if options.localOverrides {
		localPath := filepath.Join(filepath.Dir(path), FileLocal)
		local, err = readConfig(localPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("local configuration: %w", err)
		}
	}

	variables, err := variablesForConfigFile(path)
	if err != nil {
		return nil, err
	}

	return ParseProjectConfig(base, local, providers, variables)
}

// ParseProjectConfig merges configuration bytes with optional local overrides,
// resolves supplied built-ins, applies an exact provider selection, and validates
// the effective configuration. Variables never read the process environment.
// Nil local bytes mean no overrides; nil providers preserve configuration choices.
func ParseProjectConfig(baseData, localData []byte, providers []string, variables Variables) (*Config, error) {
	if err := provider.ValidateProviders(providers, ProviderNames()); err != nil {
		return nil, err
	}

	base, err := decodeConfigMap(baseData)
	if err != nil {
		return nil, fmt.Errorf("project configuration: %w", err)
	}

	local, err := decodeConfigMap(localData)
	if err != nil {
		return nil, fmt.Errorf("local configuration: %w", err)
	}

	mergeConfigMaps(base, local)
	cfg, err := resolveConfig(base, variables)
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

func decodeConfigMap(data []byte) (map[string]any, error) {
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

// mergeConfigMaps merges maps by key and replaces lists and scalar values.
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
