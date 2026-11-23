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
	"time"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
)

const SupportedVersion = "1.0"

const (
	Directory   = ".go42x"
	FileProject = "go42x.yaml"
	FileLocal   = "go42x.local.yaml"
)

// Default paths. The temporary directory is relative to Directory, and the documentation
// entrypoint to the project root.
const (
	DefaultTmpDir                  = "tmp"
	DefaultDocumentationEntrypoint = "docs/README.md"
)

// Journal defaults. The journal path is relative to Directory.
const (
	DefaultJournalPath          = "journal"
	DefaultJournalRetentionDays = 30
	DefaultJournalDir           = Directory + "/" + DefaultJournalPath
)

// reservedPaths name the content of Directory that the temporary directory and the
// journal must not use.
var reservedPaths = []string{
	"backups", "chunks", "kwb", "resources", "schemas", "skills", "upstream",
	FileProject, FileLocal, "agents.tpl.md", "kwb.ignore",
}

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
	Journal   Journal                       `yaml:"journal,omitempty"`
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
	SkillsDir string   `yaml:"skills-dir,omitempty"`
	TmpDir    string   `yaml:"tmp-dir,omitempty"`
	GoEnv     []string `yaml:"go-env,omitempty"`
}

// WithDefaults returns a copy with defaults applied to unset context settings.
func (c Context) WithDefaults() Context {
	if len(c.TmpDir) == 0 {
		c.TmpDir = DefaultTmpDir
	}
	c.TmpDir = path.Clean(c.TmpDir)
	return c
}

// TmpPath returns the directory for the temporary files of go42x and agents, relative to the
// project root.
func (c Context) TmpPath() string {
	return path.Join(Directory, c.WithDefaults().TmpDir)
}

// Journal configures the agent journal.
type Journal struct {
	// Path is the journal directory, relative to Directory.
	Path string `yaml:"path,omitempty"`
	// RetentionDays is how long an entry may go without an update before gc removes it.
	RetentionDays *int `yaml:"retention-days,omitempty"`
}

// WithDefaults returns a copy with defaults applied to unset journal settings.
func (j Journal) WithDefaults() Journal {
	if len(j.Path) == 0 {
		j.Path = DefaultJournalPath
	}
	j.Path = path.Clean(j.Path)
	if j.RetentionDays == nil {
		j.RetentionDays = new(DefaultJournalRetentionDays)
	}
	return j
}

// Dir returns the journal directory, relative to the project root.
func (j Journal) Dir() string {
	return path.Join(Directory, j.WithDefaults().Path)
}

// Retention returns how long an entry may go without an update before gc removes it.
func (j Journal) Retention() time.Duration {
	return time.Duration(*j.WithDefaults().RetentionDays) * 24 * time.Hour
}

func (j Journal) validate() error {
	if len(j.Path) > 0 {
		if err := validateDirectoryPath("journal.path", j.Path); err != nil {
			return err
		}
	}
	if j.RetentionDays != nil && *j.RetentionDays < 1 {
		return fmt.Errorf("journal.retention-days must be at least 1")
	}
	return nil
}

// validateDirectoryPath checks a directory that a setting names relative to Directory. It
// must stay inside Directory, and its first part must neither be hidden nor content that
// go42x keeps there.
func validateDirectoryPath(setting, value string) error {
	cleaned := path.Clean(value)
	first, _, _ := strings.Cut(cleaned, "/")
	if len(strings.TrimSpace(value)) == 0 || strings.ContainsAny(value, "\\\x00:\r\n") ||
		path.IsAbs(value) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
		len(value) > 4096 {
		return fmt.Errorf("%s must be a directory inside %s", setting, Directory)
	}
	if strings.HasPrefix(first, ".") || slices.Contains(reservedPaths, first) {
		return fmt.Errorf("%s must not use %s/%s, which go42x keeps", setting, Directory, first)
	}
	return nil
}

// overlaps reports whether one of two clean slash paths contains the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

type Provider struct {
	Enabled            *bool             `yaml:"enabled,omitempty"`
	Agents             []string          `yaml:"agents,omitempty"`
	AutoApproveTools   []string          `yaml:"auto-approve-tools,omitempty"`
	ApprovalPolicy     *string           `yaml:"approval-policy,omitempty"`
	MCPApproval        *string           `yaml:"mcp-approval,omitempty"`
	ProjectDocMaxBytes *int              `yaml:"project-doc-max-bytes,omitempty"`
	Bindings           map[string]string `yaml:"bindings,omitempty"`
	MCPServers         []string          `yaml:"mcp-servers,omitempty"`
}

// WorkflowMCPServers returns the MCP servers that gh-aw lists for agentic workflows, enabled
// whether or not local agents use them.
func (c *Config) WorkflowMCPServers() map[string]provider.MCPServer {
	servers := make(map[string]provider.MCPServer)
	for _, name := range c.Providers[ghaw.Name].MCPServers {
		if server, exists := c.MCP[name]; exists {
			server.Enabled = true
			servers[name] = server
		}
	}
	return servers
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

	values, err := decodeConfigMap(data)
	if err != nil {
		return nil, err
	}

	variables, err := variablesForConfigFile(path)
	if err != nil {
		return nil, err
	}

	cfg, err := resolveConfig(values, variables)
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
	if len(c.Context.TmpDir) > 0 {
		if err := validateDirectoryPath("context.tmp-dir", c.Context.TmpDir); err != nil {
			return err
		}
	}

	for _, name := range c.Context.GoEnv {
		if !goEnvNamePattern.MatchString(name) {
			return fmt.Errorf("context.go-env: invalid Go environment variable name %q", name)
		}
	}

	if err := c.Journal.validate(); err != nil {
		return err
	}
	if overlaps(c.Context.TmpPath(), c.Journal.Dir()) {
		return fmt.Errorf("context.tmp-dir and journal.path must name separate directories")
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
				if c.ProviderEnabled(providerName) && (providerName == claude.Name || providerName == crush.Name) {
					return fmt.Errorf("MCP server %s: provider %s does not support cwd", name, providerName)
				}
			}
		}
	}

	// gh-aw uses the servers it lists, not the enabled ones.
	listed := make(map[string]bool)
	for _, name := range c.Providers[ghaw.Name].MCPServers {
		server, exists := c.MCP[name]
		switch {
		case !exists:
			return fmt.Errorf("providers.gh-aw.mcp-servers: unknown MCP server %q", name)
		case listed[name]:
			return fmt.Errorf("providers.gh-aw.mcp-servers: duplicate MCP server %q", name)
		case c.ProviderEnabled(ghaw.Name) && len(server.CWD) > 0:
			return fmt.Errorf("MCP server %s: provider %s does not support cwd", name, ghaw.Name)
		}
		listed[name] = true
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
		ghaw.Name,
	}
}

func validateProvider(p Provider, name string) error {
	if p.ProjectDocMaxBytes != nil {
		if name != codex.Name {
			return fmt.Errorf("project-doc-max-bytes is only supported by codex")
		}
		if *p.ProjectDocMaxBytes < 0 {
			return fmt.Errorf("project-doc-max-bytes must be non-negative")
		}
	}

	if p.Bindings != nil && name != ghaw.Name {
		return fmt.Errorf("bindings is only supported by gh-aw")
	}
	if p.MCPServers != nil && name != ghaw.Name {
		return fmt.Errorf("mcp-servers is only supported by gh-aw")
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
