package collector

import (
	"context"
	"os/exec"
	"strings"
)

const GitCollectorName = "git"

// GitCollector collects repository identity and checkout metadata.
type GitCollector struct {
	root string
	BaseCollector
}

// NewGitCollector creates a collector for the given project root.
func NewGitCollector(root string) *GitCollector {
	return &GitCollector{
		root:          root,
		BaseCollector: NewBaseCollector(GitCollectorName, 10),
	}
}

func (c *GitCollector) Collect(ctx context.Context) (map[string]any, error) {
	result := make(map[string]any)

	root, err := c.runGitCommand(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, nil
	}
	result["root"] = strings.TrimSpace(root)

	if remote, err := c.runGitCommand(ctx, "config", "--local", "--get", "remote.origin.url"); err == nil {
		result["remote"] = strings.TrimSpace(remote)
	}
	if branch, err := c.runGitCommand(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		result["branch"] = strings.TrimSpace(branch)
	}
	if commit, err := c.runGitCommand(ctx, "rev-parse", "HEAD"); err == nil {
		result["commit"] = strings.TrimSpace(commit)
	}
	if tag, err := c.runGitCommand(ctx, "describe", "--exact-match", "--tags", "HEAD"); err == nil {
		result["tag"] = strings.TrimSpace(tag)
	}

	return result, nil
}

func (c *GitCollector) runGitCommand(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.root
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
