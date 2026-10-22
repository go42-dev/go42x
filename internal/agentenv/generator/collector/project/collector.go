package project

import (
	"context"

	"github.com/go42-dev/go42x/internal/config"
)

const (
	Name     = "project"
	priority = 5
)

// Collector collects project configuration data.
type Collector struct {
	config *config.Config
}

// New creates a collector for the given project configuration.
func New(cfg *config.Config) *Collector {
	return &Collector{config: cfg}
}

func (*Collector) Name() string {
	return Name
}

func (*Collector) Priority() int {
	return priority
}

func (c *Collector) Collect(_ context.Context) (map[string]any, error) {
	result := make(map[string]any)

	if c.config == nil {
		return result, nil
	}

	project := c.config.Project.WithDefaults()
	result["name"] = project.Name
	result["language"] = project.Language
	result["description"] = project.Description
	result["repository"] = project.Repository
	result["documentation-entrypoint"] = project.DocumentationEntrypoint

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
