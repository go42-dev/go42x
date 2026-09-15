package config

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	goEnvNamePattern      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	mcpToolNamePattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
	mcpCWDVariablePattern = regexp.MustCompile(`\$(?:[A-Za-z_]|\{|\()|%[A-Za-z_][A-Za-z0-9_]*%|^~`)
)

const (
	SupportedVersion   = "1.0"
	MCPServerTypeStdio = "stdio"
	MCPServerTypeHTTP  = "http"
	MCPServerTypeSSE   = "sse"
	LocalConfigFile    = "go42x.local.yaml"
)

type Config struct {
	Version   string               `yaml:"version"`
	Project   Project              `yaml:"project"`
	EnvVars   []string             `yaml:"env"`
	Context   Context              `yaml:"context"`
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

// Context defines the shared instructions rendered to AGENTS.md.
type Context struct {
	Template  string   `yaml:"template"`
	ChunksDir string   `yaml:"chunks-dir"`
	GoEnv     []string `yaml:"go-env,omitempty"`
}

type Provider struct {
	Enabled          *bool    `yaml:"enabled,omitempty"`
	Agents           []string `yaml:"agents,omitempty"`
	AutoApproveTools []string `yaml:"auto-approve-tools,omitempty"`
	ApprovalPolicy   *string  `yaml:"approval-policy,omitempty"`
	MCPApproval      *string  `yaml:"mcp-approval,omitempty"`
}

// ProviderEnabled reports whether a provider is configured and enabled.
// Configured providers are enabled by default when enabled is omitted.
func (c *Config) ProviderEnabled(name string) bool {
	provider, exists := c.Providers[name]
	return exists && (provider.Enabled == nil || *provider.Enabled)
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
	data, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	cfg, err := decodeConfig(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func readConfig(path string) ([]byte, error) {
	// #nosec G304 -- Reading the caller-selected project configuration is the purpose of this loader.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return data, nil
}

// decodeConfig checks field names, value types, and document boundaries without
// requiring a complete configuration. Validation follows override resolution.
func decodeConfig(data []byte) (*Config, error) {
	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("failed to parse YAML: %w", err)
		}
		return nil, fmt.Errorf("configuration must contain exactly one YAML document")
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
	if c.Version != SupportedVersion {
		return fmt.Errorf("unsupported configuration version %q; supported version is %q", c.Version, SupportedVersion)
	}

	if c.Project.Name == "" {
		return fmt.Errorf("project name is required")
	}

	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}

	if strings.TrimSpace(c.Context.Template) == "" {
		return fmt.Errorf("context.template is required; move shared instructions from providers into context")
	}
	for _, name := range c.Context.GoEnv {
		if !goEnvNamePattern.MatchString(name) {
			return fmt.Errorf("context.go-env: invalid Go environment variable name %q", name)
		}
	}

	providers := slices.Sorted(maps.Keys(c.Providers))
	for _, name := range providers {
		switch name {
		case "claude", "codex", "gemini", "crush", "copilot":
		default:
			return fmt.Errorf("unknown provider %q", name)
		}
		if err := c.Providers[name].validate(name); err != nil {
			return fmt.Errorf("providers.%s: %w", name, err)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(c.MCP)) {
		server := c.MCP[name]
		if server.Name == "" {
			return fmt.Errorf("MCP server %s: name is required", name)
		}
		if err := server.validate(); err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}
		if server.Enabled && server.CWD != "" {
			for _, provider := range providers {
				if c.ProviderEnabled(provider) && (provider == "claude" || provider == "crush") {
					return fmt.Errorf("MCP server %s: provider %s does not support cwd", name, provider)
				}
			}
		}
	}

	return nil
}

func (p Provider) validate(name string) error {
	if p.Agents != nil && name != "claude" {
		return fmt.Errorf("agents is only supported by claude")
	}
	if p.AutoApproveTools != nil && name != "claude" && name != "gemini" && name != "crush" {
		return fmt.Errorf("auto-approve-tools is only supported by claude, gemini, and crush")
	}
	for i, tool := range p.AutoApproveTools {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("auto-approve-tools[%d] must not be blank", i)
		}
	}
	if p.ApprovalPolicy != nil {
		if name != "codex" {
			return fmt.Errorf("approval-policy is only supported by codex")
		}
		switch *p.ApprovalPolicy {
		case "on-request", "never":
		default:
			return fmt.Errorf("invalid approval-policy %q: use on-request or never", *p.ApprovalPolicy)
		}
	}
	if p.MCPApproval != nil {
		if name != "codex" {
			return fmt.Errorf("mcp-approval is only supported by codex")
		}
		switch *p.MCPApproval {
		case "auto", "prompt", "writes", "approve":
		default:
			return fmt.Errorf("invalid mcp-approval %q: use auto, prompt, writes, or approve", *p.MCPApproval)
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

	if s.CWD != "" {
		if s.Transport() != MCPServerTypeStdio {
			return fmt.Errorf("cwd is only supported for stdio transport")
		}
		if strings.TrimSpace(s.CWD) == "" || strings.ContainsRune(s.CWD, '\x00') {
			return fmt.Errorf("cwd must be a nonblank path without NUL characters")
		}
		if mcpCWDVariablePattern.MatchString(s.CWD) {
			return fmt.Errorf("cwd must be a literal path; environment variables and home expansion are not supported")
		}
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
