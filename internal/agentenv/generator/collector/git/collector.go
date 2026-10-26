package git

import (
	"context"
	"net/url"
	"os/exec"
	"strings"
)

const (
	Name     = "git"
	priority = 10
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// Collector collects repository identity and checkout metadata.
type Collector struct {
	root string
}

// New creates a collector for the given project root.
func New(root string) *Collector {
	return &Collector{root: root}
}

func (*Collector) Name() string {
	return Name
}

func (*Collector) Priority() int {
	return priority
}

func (c *Collector) Collect(ctx context.Context) (map[string]any, error) {
	result := make(map[string]any)

	root, err := c.runGitCommand(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, nil
	}

	result["root"] = strings.TrimSpace(root)

	if remote, err := c.runGitCommand(ctx, "config", "--local", "--get", "remote.origin.url"); err == nil {
		remote = sanitizeRemote(remote)
		if len(remote) > 0 {
			result["remote"] = remote
		}
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

// sanitizeRemote removes HTTP authentication without exposing malformed URLs.
func sanitizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	parsed, err := url.Parse(remote)
	if err != nil {
		if strings.Contains(remote, "://") {
			return ""
		}
		return remote
	}
	if strings.EqualFold(parsed.Scheme, schemeHTTP) || strings.EqualFold(parsed.Scheme, schemeHTTPS) {
		parsed.User = nil
		return parsed.String()
	}

	return remote
}

func (c *Collector) runGitCommand(ctx context.Context, args ...string) (string, error) {
	// #nosec G204 -- All callers supply fixed Git arguments; no shell interprets repository data.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.root
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}
