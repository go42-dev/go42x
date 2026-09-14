// Package assets provides the files bundled into go42x.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:agentenv/template/*
var agentEnvTemplates embed.FS

//go:embed agentenv/go42x.schema.json
var agentEnvSchema string

// AgentEnvTemplates returns the bundled files rooted at the template directory.
func AgentEnvTemplates() (fs.FS, error) {
	return fs.Sub(agentEnvTemplates, "agentenv/template")
}

// AgentEnvSchema returns the JSON schema for go42x.yaml.
func AgentEnvSchema() string {
	return agentEnvSchema
}
