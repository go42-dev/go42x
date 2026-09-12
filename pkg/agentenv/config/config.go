package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var mcpToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

const (
	MCPServerTypeStdio = "stdio"
	MCPServerTypeHTTP  = "http"
	MCPServerTypeSSE   = "sse"
)

type Config struct {
	Version   string               `yaml:"version"`
	Project   Project              `yaml:"project"`
	EnvVars   []string             `yaml:"env"`
	Providers map[string]Provider  `yaml:"providers"`
	MCP       map[string]MCPServer `yaml:"mcp"`
}

type Project struct {
	Name        string            `yaml:"name"`
	Language    string            `yaml:"language"`
	Description string            `yaml:"description"`
	Tags        []string          `yaml:"tags"`
	Metadata    map[string]string `yaml:"metadata"`
}

type Provider struct {
	Template  string   `yaml:"template"`
	Output    string   `yaml:"output"`
	Chunks    []string `yaml:"chunks"`
	Modes     []string `yaml:"modes"`
	Workflows []string `yaml:"workflows"`
	Agents    []string `yaml:"agents"`
	Hooks     []string `yaml:"hooks"`
	Tools     []string `yaml:"tools"`
}

type MCPServer struct {
	Enabled bool              `yaml:"enabled"`
	Type    string            `yaml:"type"`
	Name    string            `yaml:"name"`
	URL     string            `yaml:"url"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	Tools   []string          `yaml:"tools"`
	Headers map[string]string `yaml:"headers"`
	CWD     string            `yaml:"cwd"`
}

func (s MCPServer) Transport() string {
	if s.Type == "" {
		return MCPServerTypeStdio
	}
	return s.Type
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &config, nil
}

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}

	if c.Version == "" {
		return fmt.Errorf("version is required")
	}

	if c.Project.Name == "" {
		return fmt.Errorf("project name is required")
	}

	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}

	for name, provider := range c.Providers {
		switch name {
		case "claude", "gemini", "crush", "copilot":
		default:
			return fmt.Errorf("unknown provider %q", name)
		}
		if provider.Template == "" {
			return fmt.Errorf("provider %s: template is required", name)
		}
		if provider.Output == "" {
			return fmt.Errorf("provider %s: output is required", name)
		}
	}

	for name, server := range c.MCP {
		if server.Name == "" {
			return fmt.Errorf("MCP server %s: name is required", name)
		}
		if err := server.validate(); err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}
	}

	return nil
}

func (s MCPServer) validate() error {
	switch s.Transport() {
	case MCPServerTypeStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
	case MCPServerTypeHTTP, MCPServerTypeSSE:
		u, err := url.Parse(s.URL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("a valid HTTP(S) URL is required for %s transport", s.Type)
		}
	default:
		return fmt.Errorf("invalid type %s", s.Type)
	}

	seen := make(map[string]bool, len(s.Tools))
	for _, tool := range s.Tools {
		if strings.HasPrefix(tool, "mcp__") || !mcpToolNamePattern.MatchString(tool) {
			return fmt.Errorf("invalid tool %q: use the raw MCP tool name without a client prefix", tool)
		}
		if seen[tool] {
			return fmt.Errorf("duplicate tool %q", tool)
		}
		seen[tool] = true
	}
	return nil
}
