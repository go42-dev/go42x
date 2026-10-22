package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	Name     = "golang"
	priority = 25
)

// Collector collects the project's Go requirement and effective Go settings.
type Collector struct {
	root    string
	envVars []string
	timeout time.Duration
}

// New adds extraEnv to the standard toolchain, path, and build settings.
func New(root string, extraEnv []string) *Collector {
	if len(root) == 0 {
		root = "."
	}
	variables := append([]string{
		"GOVERSION", "GOTOOLCHAIN", "GOROOT", "GOPATH", "GOBIN", "GOCACHE",
		"GOMODCACHE", "GOMOD", "GOWORK", "GOOS", "GOARCH", "CGO_ENABLED",
	}, extraEnv...)
	slices.Sort(variables)
	return &Collector{
		root:    root,
		envVars: slices.Compact(variables),
		timeout: 3 * time.Second,
	}
}

func (*Collector) Name() string {
	return Name
}

func (*Collector) Priority() int {
	return priority
}

func (c *Collector) Collect(ctx context.Context) (map[string]any, error) {
	result := make(map[string]any)
	goVersion, moduleErr := c.moduleVersion()
	if len(goVersion) > 0 {
		result["go_version"] = goVersion
	}
	env, envErr := c.goEnv(ctx)
	if len(env) > 0 {
		result["env"] = env
	}
	return result, errors.Join(moduleErr, envErr)
}

func (c *Collector) moduleVersion() (string, error) {
	// Read the Go requirement even when the Go executable is unavailable.
	// #nosec G304 -- The caller explicitly selects the project whose go.mod is inspected.
	data, err := os.ReadFile(filepath.Join(c.root, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}

		return "", fmt.Errorf("read project go.mod: %w", err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" && version.IsValid("go"+fields[1]) {
			return fields[1], nil
		}
	}
	return "", nil
}

func (c *Collector) goEnv(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// The option terminator keeps configured names from being interpreted as flags.
	args := append([]string{"env", "-json", "--"}, c.envVars...)
	// #nosec G204 -- Executes Go directly; variable names follow -- and no shell is used.
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = c.root
	cmd.WaitDelay = 100 * time.Millisecond

	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read go env: %w", errors.Join(ctx.Err(), err))
	}

	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decode go env: %w", err)
	}

	// Publish only requested variables, even if a Go wrapper returns extra fields.
	result := make(map[string]string)
	for _, key := range c.envVars {
		value := values[key]
		if len(value) == 0 {
			continue
		}

		result[key] = value
	}

	return result, nil
}
