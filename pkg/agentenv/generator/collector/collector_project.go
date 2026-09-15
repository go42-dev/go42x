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

func (c *ProjectCollector) Collect(_ context.Context) (map[string]any, error) {
	result := make(map[string]any)

	if c.config == nil {
		return result, nil
	}

	result["name"] = c.config.Project.Name
	result["language"] = c.config.Project.Language
	result["description"] = c.config.Project.Description

	// Add tags as array
	if len(c.config.Project.Tags) > 0 {
		result["tags"] = c.config.Project.Tags
	}

	// Add metadata as nested map
	if len(c.config.Project.Metadata) > 0 {
		result["metadata"] = c.config.Project.Metadata
	}

	return result, nil
}
