package environment

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
)

const (
	Name     = "environment"
	priority = 20
)

// Collector collects runtime environment information.
type Collector struct {
	root    string
	envVars []string
}

// New creates a collector for the given project root.
func New(envVars []string, root string) *Collector {
	return &Collector{
		root:    root,
		envVars: envVars,
	}
}

func (*Collector) Name() string {
	return Name
}

func (*Collector) Priority() int {
	return priority
}

func (c *Collector) Collect(_ context.Context) (map[string]any, error) {
	result := make(map[string]any)

	result["is_ci"] = os.Getenv("CI") == "true"
	result["ci_mode"] = os.Getenv("CI")

	result["os"] = runtime.GOOS
	result["arch"] = runtime.GOARCH

	root := c.root
	if len(root) == 0 {
		root = "."
	}
	if wd, err := filepath.Abs(root); err == nil {
		result["working_dir"] = wd
	}

	variables := make(map[string]string)
	for _, key := range c.envVars {
		if value := os.Getenv(key); len(value) > 0 {
			variables[key] = value
		}
	}
	if len(variables) > 0 {
		result["variables"] = variables
	}

	return result, nil
}
