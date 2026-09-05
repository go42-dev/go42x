package collector

import (
	"context"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const ProjectCollectorName = "project"

// ProjectCollector collects project configuration data
type ProjectCollector struct {
	BaseCollector
	config *config.Config
}

func NewProjectCollector(cfg *config.Config) *ProjectCollector {
	return &ProjectCollector{
		BaseCollector: NewBaseCollector(ProjectCollectorName, 5),
		config:        cfg,
	}
}

func (c *ProjectCollector) Collect(_ context.Context) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	if c.config == nil {
		return result, nil
	}

	result["name"] = c.config.Project.Name
	result["language"] = c.config.Project.Language
	result["description"] = c.config.Project.Description
	result["version"] = c.config.Version

	// Add tags as array
	if len(c.config.Project.Tags) > 0 {
		result["tags"] = c.config.Project.Tags
	}

	// Add metadata as nested map
	if len(c.config.Project.Metadata) > 0 {
		result["metadata"] = c.config.Project.Metadata
	}

	// Add provider information
	providers := make([]string, 0, len(c.config.Providers))
	for name := range c.config.Providers {
		providers = append(providers, name)
	}
	result["providers"] = providers

	// Add MCP server information
	mcpServers := make([]map[string]interface{}, 0)
	for name, server := range c.config.MCP {
		if server.Enabled {
			mcpServers = append(mcpServers, map[string]interface{}{
				"name":    name,
				"type":    server.Type,
				"command": server.Command,
			})
		}
	}
	if len(mcpServers) > 0 {
		result["mcp_servers"] = mcpServers
	}

	return result, nil
}
