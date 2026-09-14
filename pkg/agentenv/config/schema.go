package config

import "github.com/go42-dev/go42x/assets"

// Schema returns the JSON schema for go42x.yaml.
func Schema() string {
	return assets.AgentEnvSchema()
}
