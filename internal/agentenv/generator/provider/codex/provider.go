package codex

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const Name = "codex"

// ConfigFile is the project-local Codex configuration path.
const ConfigFile = ".codex/config.toml"

// DefaultInstructionBytes is Codex's default project instruction budget.
const DefaultInstructionBytes = 32 * 1024

const keyProjectDocMaxBytes = "project_doc_max_bytes"

const envHome = "CODEX_HOME"

// MCPServer represents a server in .codex/config.toml.
// @see https://developers.openai.com/codex/mcp
type MCPServer struct {
	Command                  string            `toml:"command,omitempty"`
	Args                     []string          `toml:"args,omitempty"`
	Env                      map[string]string `toml:"env,omitempty"`
	EnvVars                  []string          `toml:"env_vars,omitempty"`
	CWD                      string            `toml:"cwd,omitempty"`
	URL                      string            `toml:"url,omitempty"`
	HTTPHeaders              map[string]string `toml:"http_headers,omitempty"`
	EnvHTTPHeaders           map[string]string `toml:"env_http_headers,omitempty"`
	BearerTokenEnvVar        string            `toml:"bearer_token_env_var,omitempty"`
	DefaultToolsApprovalMode string            `toml:"default_tools_approval_mode,omitempty"`
}

// Settings contains the inputs needed to generate this provider's files.
type Settings struct {
	OutputDir          string
	MCPServers         map[string]provider.MCPServer
	ApprovalPolicy     *string
	MCPApproval        *string
	ProjectDocMaxBytes *int
}

type Provider struct {
	settings Settings
}

func New(
	_ *slog.Logger,
	_ provider.TemplateEngineAccessor,
	settings Settings,
	_ string,
) *Provider {
	return &Provider{settings: settings}
}

func (p *Provider) Prepare(plan *fspatch.Plan, _ map[string]any) error {
	if err := p.prepareConfigFiles(plan); err != nil {
		return fmt.Errorf("failed to prepare config files: %w", err)
	}
	return nil
}

func (p *Provider) prepareConfigFiles(plan *fspatch.Plan) error {
	servers := make(map[string]MCPServer)
	for _, name := range slices.Sorted(maps.Keys(p.settings.MCPServers)) {
		server := p.settings.MCPServers[name]
		if !server.Enabled {
			continue
		}

		entry, err := codexMCPServer(server)
		if err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}
		if p.settings.MCPApproval != nil {
			entry.DefaultToolsApprovalMode = *p.settings.MCPApproval
		}
		servers[name] = entry
	}
	// Decode generated entries to maps as well, so unchanged settings compare
	// equally regardless of TOML struct field order or existing file formatting.
	serverContent, err := toml.Marshal(servers)
	if err != nil {
		return fmt.Errorf("failed to encode codex MCP settings: %w", err)
	}

	serverSettings := make(map[string]any)
	if err := toml.Unmarshal(serverContent, &serverSettings); err != nil {
		return fmt.Errorf("failed to normalize codex MCP settings: %w", err)
	}

	path := filepath.Join(p.settings.OutputDir, ConfigFile)
	previous, exists, err := plan.Read(path)
	if err != nil {
		return err
	}

	settings := make(map[string]any)
	if exists {
		if err := toml.Unmarshal(previous, &settings); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
	}
	before, err := toml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to encode existing codex settings: %w", err)
	}
	// Agentenv owns the MCP section and explicitly configured settings; retain other values.
	settings["mcp_servers"] = serverSettings
	if p.settings.ApprovalPolicy != nil {
		settings["approval_policy"] = *p.settings.ApprovalPolicy
	}
	if p.settings.ProjectDocMaxBytes != nil {
		settings[keyProjectDocMaxBytes] = *p.settings.ProjectDocMaxBytes
	}
	content, err := toml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to encode codex settings: %w", err)
	}
	if exists && bytes.Equal(before, content) {
		content = previous
	}
	if err := plan.Write(path, content, fspatch.KindSettings, false); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", path, err)
	}

	return nil
}

// InstructionLimit resolves the user and project instruction budget, in that order.
func InstructionLimit(root string) (int, error) {
	home := os.Getenv(envHome)
	if len(home) == 0 {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return 0, fmt.Errorf("locate Codex configuration: %w", err)
		}
		home = filepath.Join(userHome, filepath.Dir(ConfigFile))
	}

	limit := DefaultInstructionBytes
	for _, path := range []string{filepath.Join(home, filepath.Base(ConfigFile)), filepath.Join(root, ConfigFile)} {
		// #nosec G304 G703 -- The trusted local project and CODEX_HOME select Codex configuration files.
		content, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read Codex configuration %s: %w", path, err)
		}

		value, err := ParseInstructionLimit(string(content))
		if err != nil {
			return 0, fmt.Errorf("check Codex configuration %s: %w", path, err)
		}
		if value != nil {
			limit = *value
		}
	}
	return limit, nil
}

// ParseInstructionLimit reads the optional project instruction budget from TOML.
func ParseInstructionLimit(content string) (*int, error) {
	settings := make(map[string]any)
	if err := toml.Unmarshal([]byte(content), &settings); err != nil {
		return nil, fmt.Errorf("parse Codex configuration: %w", err)
	}
	value, exists := settings[keyProjectDocMaxBytes]
	if !exists {
		return nil, nil
	}

	limit, ok := value.(int64)
	if !ok || limit < 0 || int64(int(limit)) != limit {
		return nil, fmt.Errorf("%s must be a non-negative integer", keyProjectDocMaxBytes)
	}
	return new(int(limit)), nil
}

func codexMCPServer(server provider.MCPServer) (MCPServer, error) {
	var entry MCPServer
	switch server.Transport() {
	case provider.MCPServerTypeStdio:
		if codexEnvExpression.MatchString(server.Command) || codexEnvExpression.MatchString(server.CWD) {
			return entry, fmt.Errorf(
				"codex command and cwd must be literal paths; use a wrapper command for environment expansion",
			)
		}
		if slices.ContainsFunc(server.Args, codexEnvExpression.MatchString) {
			return entry, fmt.Errorf(
				"codex args must be literal values; use a wrapper command for environment expansion",
			)
		}
		entry.Command = server.Command
		entry.Args = server.Args
		entry.CWD = server.CWD
		entry.Env = make(map[string]string)
		for _, key := range slices.Sorted(maps.Keys(server.Env)) {
			value := server.Env[key]
			if name, ok := codexEnvReference(value); ok && name == key {
				entry.EnvVars = append(entry.EnvVars, name)
			} else if codexEnvExpression.MatchString(value) {
				return entry, fmt.Errorf(
					"codex env.%s must use a literal or $%s; configure a wrapper command for renamed or composed variables",
					key,
					key,
				)
			} else {
				entry.Env[key] = value
			}
		}
	case provider.MCPServerTypeHTTP:
		if codexEnvExpression.MatchString(server.URL) {
			return entry, fmt.Errorf("codex URL must be literal; use headers for environment-based authentication")
		}
		entry.URL = server.URL
		entry.HTTPHeaders = make(map[string]string)
		entry.EnvHTTPHeaders = make(map[string]string)
		for _, key := range slices.Sorted(maps.Keys(server.Headers)) {
			value := server.Headers[key]
			if strings.EqualFold(key, "Authorization") && strings.HasPrefix(value, "Bearer ") {
				if name, ok := codexEnvReference(strings.TrimPrefix(value, "Bearer ")); ok {
					entry.BearerTokenEnvVar = name
					continue
				}
			}
			if name, ok := codexEnvReference(value); ok {
				entry.EnvHTTPHeaders[key] = name
			} else if codexEnvExpression.MatchString(value) {
				return entry, fmt.Errorf(
					"codex header %s must use a literal or a reference to the complete header value",
					key,
				)
			} else {
				entry.HTTPHeaders[key] = value
			}
		}
	default:
		return entry, fmt.Errorf(
			"codex does not support %s transport; use stdio or streamable HTTP",
			server.Transport(),
		)
	}
	return entry, nil
}

var (
	codexEnvName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	codexEnvExpression = regexp.MustCompile(`\$(?:[A-Za-z_]|\{)`)
)

// codexEnvReference extracts a runtime environment variable name without reading secrets.
func codexEnvReference(value string) (string, bool) {
	name, ok := strings.CutPrefix(value, "$")
	if !ok {
		return "", false
	}
	if strings.HasPrefix(name, "{") && strings.HasSuffix(name, "}") {
		name = name[1 : len(name)-1]
	}
	return name, codexEnvName.MatchString(name)
}

// MCPToolName formats a server tool name for this provider.
func MCPToolName(serverName, toolName string) string {
	return fmt.Sprintf("mcp__%s__%s", serverName, toolName)
}
