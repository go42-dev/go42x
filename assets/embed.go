// Package assets provides the files bundled into go42x.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed agentenv/go42x.yaml agentenv/agents.tpl.md
//go:embed agentenv/.vendor.yaml agentenv/.vendor.lock.yaml
//go:embed all:agentenv/chunks all:agentenv/upstream
var agentEnvTemplates embed.FS

// AgentEnvTemplates returns the bundled sources installed into .go42x.
func AgentEnvTemplates() (fs.FS, error) {
	return fs.Sub(agentEnvTemplates, "agentenv")
}

//go:embed agentenv/.gitignore.tpl
var agentEnvGitIgnore string

// AgentEnvGitIgnore returns the managed .gitignore block for agentenv.
func AgentEnvGitIgnore() string {
	return agentEnvGitIgnore
}

//go:embed kwb/kwb.ignore.tpl
var kwbIgnore string

// KWBIgnore returns the starter template for .go42x/kwb.ignore.
func KWBIgnore() string {
	return kwbIgnore
}

//go:embed vendoring/.vendor.yaml.tpl
var vendorManifestTemplate string

// VendorManifestTemplate returns the starter template for .vendor.yaml.
func VendorManifestTemplate() string {
	return vendorManifestTemplate
}
