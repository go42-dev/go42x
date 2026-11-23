// Package assets provides the files bundled into go42x.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed agentenv/go42x.yaml agentenv/agents.tpl.md
//go:embed agentenv/.vendor.yaml agentenv/.vendor.lock.yaml
//go:embed all:agentenv/chunks all:agentenv/resources all:agentenv/skills all:agentenv/upstream
//go:embed all:agentenv/schemas
var agentEnvTemplates embed.FS

// AgentEnvTemplates returns the bundled sources installed into .go42x.
func AgentEnvTemplates() (fs.FS, error) {
	return fs.Sub(agentEnvTemplates, "agentenv")
}
