package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
)

const (
	SupportedVersion               = "1.0"
	Directory                      = ".go42x"
	ProjectConfigFile              = "go42x.yaml"
	LocalConfigFile                = "go42x.local.yaml"
	DefaultTempDir                 = ".build"
	DefaultDocumentationEntrypoint = "docs/README.md"
)

var (
	goEnvNamePattern      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	bindingNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	mcpToolNamePattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
	mcpCWDVariablePattern = regexp.MustCompile(`\$(?:[A-Za-z_]|\{|\()|%[A-Za-z_][A-Za-z0-9_]*%|^~`)
)

type Config struct {
	Version   string                        `yaml:"version"`
	Project   Project                       `yaml:"project"`
	EnvVars   []string                      `yaml:"env"`
	Context   Context                       `yaml:"context"`
	Providers map[string]Provider           `yaml:"providers"`
	MCP       map[string]provider.MCPServer `yaml:"mcp"`
}

type Project struct {
	Name                    string            `yaml:"name"`
	Language                string            `yaml:"language"`
	Description             string            `yaml:"description"`
	Repository              string            `yaml:"repository,omitempty"`
	DocumentationEntrypoint string            `yaml:"documentation-entrypoint,omitempty"`
	Tags                    []string          `yaml:"tags"`
	Metadata                map[string]string `yaml:"metadata"`
}

// WithDefaults returns a copy with the documentation entrypoint default applied.
func (p Project) WithDefaults() Project {
	if len(p.DocumentationEntrypoint) == 0 {
		p.DocumentationEntrypoint = DefaultDocumentationEntrypoint
	}
	p.DocumentationEntrypoint = path.Clean(p.DocumentationEntrypoint)
	return p
}

// Context defines the shared instructions rendered to AGENTS.md.
type Context struct {
	Template  string   `yaml:"template"`
	ChunksDir string   `yaml:"chunks-dir"`
	TempDir   string   `yaml:"temp-dir,omitempty"`
	GoEnv     []string `yaml:"go-env,omitempty"`
}

// WithDefaults returns a copy with defaults applied to unset context settings.
func (c Context) WithDefaults() Context {
	if len(c.TempDir) == 0 {
		c.TempDir = DefaultTempDir
	}
	return c
}

type Provider struct {
	Enabled          *bool             `yaml:"enabled,omitempty"`
	Agents           []string          `yaml:"agents,omitempty"`
	AutoApproveTools []string          `yaml:"auto-approve-tools,omitempty"`
	ApprovalPolicy   *string           `yaml:"approval-policy,omitempty"`
	MCPApproval      *string           `yaml:"mcp-approval,omitempty"`
	Bindings         map[string]string `yaml:"bindings,omitempty"`
}

// ProviderEnabled reports whether a provider is configured and enabled.
// Configured providers are enabled by default when enabled is omitted.
func (c *Config) ProviderEnabled(name string) bool {
	provider, exists := c.Providers[name]
	return exists && (provider.Enabled == nil || *provider.Enabled)
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
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
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

	if len(c.Version) == 0 {
		return fmt.Errorf("version is required")
	}
	if c.Version != SupportedVersion {
		return fmt.Errorf("unsupported configuration version %q; supported version is %q", c.Version, SupportedVersion)
	}

	if len(c.Project.Name) == 0 {
		return fmt.Errorf("project name is required")
	}

	if entrypoint := c.Project.DocumentationEntrypoint; len(entrypoint) > 0 {
		cleaned := path.Clean(entrypoint)
		if len(strings.TrimSpace(entrypoint)) == 0 || strings.ContainsAny(entrypoint, "\\\x00:") ||
			path.IsAbs(entrypoint) || cleaned == ".." || strings.HasPrefix(cleaned, "../") || len(entrypoint) > 4096 {
			return fmt.Errorf("project.documentation-entrypoint must be a path inside the project root")
		}

		if ext := strings.ToLower(path.Ext(cleaned)); ext != ".md" && ext != ".markdown" {
			return fmt.Errorf("project.documentation-entrypoint must be a Markdown file")
		}
	}

	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}

	if len(strings.TrimSpace(c.Context.Template)) == 0 {
		return fmt.Errorf("context.template is required; move shared instructions from providers into context")
	}
	if len(c.Context.TempDir) > 0 &&
		(len(strings.TrimSpace(c.Context.TempDir)) == 0 || strings.ContainsRune(c.Context.TempDir, '\x00')) {
		return fmt.Errorf("context.temp-dir must be a nonblank path without NUL characters")
	}

	for _, name := range c.Context.GoEnv {
		if !goEnvNamePattern.MatchString(name) {
			return fmt.Errorf("context.go-env: invalid Go environment variable name %q", name)
		}
	}

	providers := slices.Sorted(maps.Keys(c.Providers))
	if err := provider.ValidateProviders(providers, ProviderNames()); err != nil {
		return err
	}

	for _, name := range providers {
		if err := validateProvider(c.Providers[name], name); err != nil {
			return fmt.Errorf("providers.%s: %w", name, err)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(c.MCP)) {
		server := c.MCP[name]
		if len(server.Name) == 0 {
			return fmt.Errorf("MCP server %s: name is required", name)
		}

		if err := validateMCPServer(server); err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}
		if server.Enabled && len(server.CWD) > 0 {
			for _, providerName := range providers {
				if c.ProviderEnabled(providerName) &&
					(providerName == claude.Name || providerName == crush.Name || providerName == ghaw.Name) {
					return fmt.Errorf("MCP server %s: provider %s does not support cwd", name, providerName)
				}
			}
		}
	}

	return nil
}

func validateMCPServer(s provider.MCPServer) error {
	if s.Container != nil {
		if s.Transport() != provider.MCPServerTypeStdio {
			return fmt.Errorf("container is only supported for stdio transport")
		}
		if len(strings.TrimSpace(s.Container.Image)) == 0 || len(strings.TrimSpace(s.Container.Command)) == 0 {
			return fmt.Errorf("container: image and command are required")
		}
	}

	switch s.Transport() {
	case provider.MCPServerTypeStdio:
		if len(strings.TrimSpace(s.Command)) == 0 {
			return fmt.Errorf("command is required for stdio transport")
		}
	case provider.MCPServerTypeHTTP, provider.MCPServerTypeSSE:
		u, err := url.Parse(s.URL)
		if err != nil || len(u.Host) == 0 || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("a valid HTTP(S) URL is required for %s transport", s.Type)
		}
	default:
		return fmt.Errorf("invalid type %s", s.Type)
	}

	if len(s.CWD) > 0 {
		if s.Transport() != provider.MCPServerTypeStdio {
			return fmt.Errorf("cwd is only supported for stdio transport")
		}
		if len(strings.TrimSpace(s.CWD)) == 0 || strings.ContainsRune(s.CWD, '\x00') {
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

// ProviderNames returns the supported providers, using their canonical names.
func ProviderNames() []string {
	return []string{
		claude.Name,
		codex.Name,
		antigravity.Name,
		crush.Name,
		copilot.Name,
		ghaw.Name,
	}
}

func validateProvider(p Provider, name string) error {
	if p.Bindings != nil && name != ghaw.Name {
		return fmt.Errorf("bindings is only supported by gh-aw")
	}

	for _, reference := range slices.Sorted(maps.Keys(p.Bindings)) {
		if !bindingNamePattern.MatchString(reference) {
			return fmt.Errorf("bindings: invalid reference name %q", reference)
		}
		if len(strings.TrimSpace(p.Bindings[reference])) == 0 {
			return fmt.Errorf("bindings.%s must not be blank", reference)
		}
	}

	if p.Agents != nil && name != claude.Name {
		return fmt.Errorf("agents is only supported by claude")
	}
	if p.AutoApproveTools != nil && name != claude.Name && name != crush.Name {
		return fmt.Errorf("auto-approve-tools is only supported by claude and crush")
	}

	for i, tool := range p.AutoApproveTools {
		if len(strings.TrimSpace(tool)) == 0 {
			return fmt.Errorf("auto-approve-tools[%d] must not be blank", i)
		}
	}
	if p.ApprovalPolicy != nil {
		if name != codex.Name {
			return fmt.Errorf("approval-policy is only supported by codex")
		}

		switch *p.ApprovalPolicy {
		case "on-request", "never":
		default:
			return fmt.Errorf("invalid approval-policy %q: use on-request or never", *p.ApprovalPolicy)
		}
	}
	if p.MCPApproval != nil {
		if name != codex.Name {
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
