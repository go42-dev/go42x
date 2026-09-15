package collector

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
)

const EnvironmentCollectorName = "environment"

// EnvironmentCollector collects runtime environment information
type EnvironmentCollector struct {
	root string
	BaseCollector
	envVars []string
}

// NewEnvironmentCollector creates a collector for the given project root.
func NewEnvironmentCollector(envVars []string, root string) *EnvironmentCollector {
	return &EnvironmentCollector{
		root:          root,
		BaseCollector: NewBaseCollector(EnvironmentCollectorName, 20),
		envVars:       envVars,
	}
}

func (c *EnvironmentCollector) Collect(_ context.Context) (map[string]any, error) {
	result := make(map[string]any)

	result["is_ci"] = os.Getenv("CI") == "true"
	result["ci_mode"] = os.Getenv("CI")

	result["os"] = runtime.GOOS
	result["arch"] = runtime.GOARCH

	root := c.root
	if root == "" {
		root = "."
	}
	if wd, err := filepath.Abs(root); err == nil {
		result["working_dir"] = wd
	}

	variables := make(map[string]string)
	for _, key := range c.envVars {
		if value := os.Getenv(key); value != "" {
			variables[key] = value
		}
	}
	if len(variables) > 0 {
		result["variables"] = variables
	}

	return result, nil
}
