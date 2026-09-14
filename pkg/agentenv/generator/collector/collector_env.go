package collector

import (
	"context"
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	// Read the project's Go requirement without invoking the Go toolchain.
	// #nosec G304 -- The caller explicitly selects the local project whose go.mod is inspected.
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read project go.mod: %w", err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" && version.IsValid("go"+fields[1]) {
			result["go_required_version"] = fields[1]
			// Keep existing custom templates compatible with the original field.
			result["go_version"] = fields[1]
			break
		}
	}

	return result, nil
}
