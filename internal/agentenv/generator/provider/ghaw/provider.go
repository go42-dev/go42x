package ghaw

import (
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const Name = "gh-aw"

// MCPFile declares the enabled MCP servers for agentic workflows to import.
const MCPFile = ".github/workflows/900-aw-mcp.md"

// JournalFile restores the agent journal before the agent runs and saves it after threat
// detection passes, when the JOURNAL_REPOSITORY variable names a journal repository.
const JournalFile = ".github/workflows/901-aw-journal.md"

// imports maps each workflow import to its bundled template.
var imports = []struct{ file, template string }{
	{MCPFile, "900-aw-mcp.tpl.md"},
	{JournalFile, "901-aw-journal.tpl.md"},
}

const (
	commandDocker = "docker"
	commandNPX    = "npx"
	commandUVX    = "uvx"
)

const flagWorkDir = "--workdir"

const mcpServerNamePrefix = "mcp-"

var (
	ghAWServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// ghAWBindingPattern matches native expressions first to preserve them, including quoted strings.
	ghAWBindingPattern = regexp.MustCompile(
		`(?s)\$\{\{(?:'[^']*'|[^'])*?\}\}|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*`,
	)
	ghAWEnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	ghAWHeaderPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// MCPServer contains the fields supported by gh-aw's MCP declarations.
// @see https://github.github.com/gh-aw/guides/mcps/
type MCPServer struct {
	Container      string            `yaml:"container,omitempty"`
	Version        string            `yaml:"version,omitempty"`
	Entrypoint     string            `yaml:"entrypoint,omitempty"`
	EntrypointArgs []string          `yaml:"entrypointArgs,omitempty"`
	Mounts         []string          `yaml:"mounts,omitempty"`
	Type           string            `yaml:"type"`
	Command        string            `yaml:"command,omitempty"`
	Args           []string          `yaml:"args,omitempty"`
	Env            map[string]string `yaml:"env,omitempty"`
	URL            string            `yaml:"url,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`
	Allowed        []string          `yaml:"allowed"`
}

// Settings contains the inputs needed to generate this provider's files.
type Settings struct {
	OutputDir  string
	MCPServers map[string]provider.MCPServer
	Bindings   map[string]string
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

// SkillDirs lists the skill directories of every engine a workflow may run.
func (p *Provider) SkillDirs() []string {
	return []string{claude.SkillsDir, provider.SharedSkillsDir}
}

// Prepare renders the bundled workflow imports with the template context, which gains the
// enabled MCP servers in gh-aw form as .ghaw.mcp_servers.
func (p *Provider) Prepare(plan *fspatch.Plan, ctxData map[string]any) error {
	servers := make(map[string]MCPServer)
	for name, server := range p.settings.MCPServers {
		if !server.Enabled {
			continue
		}

		if !ghAWServerNamePattern.MatchString(name) {
			return fmt.Errorf(
				"MCP server %s: gh-aw requires names containing only letters, digits, underscores, or hyphens",
				name,
			)
		}

		container := server.Container
		// Adapt a copy; other providers keep the shared runner command and environment.
		if container != nil {
			if server.Transport() != provider.MCPServerTypeStdio {
				return fmt.Errorf("MCP server %s: container requires a stdio server", name)
			}

			server.Command = commandDocker
			server.Args = nil
			env := make(map[string]string, len(server.Env)+len(container.Env))
			maps.Copy(env, server.Env)
			maps.Copy(env, container.Env)
			server.Env = env
		}
		entry, err := ghAWMCPServer(server)
		if err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}
		if container != nil {
			entry.Command = ""
			entry.Container = container.Image
			entry.Version = container.Version
			entry.Entrypoint = container.Command
			entry.EntrypointArgs = slices.Clone(container.Args)
			entry.Mounts = slices.Clone(container.Mounts)
			if len(container.WorkDir) > 0 {
				entry.Args = []string{flagWorkDir, container.WorkDir}
			}
		}
		if err := p.resolveMCPBindings(&entry); err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}

		servers[mcpServerNamePrefix+name] = entry
	}

	data := maps.Clone(ctxData)
	if data == nil {
		data = make(map[string]any)
	}
	data["ghaw"] = map[string]any{"mcp_servers": servers}
	for _, workflowImport := range imports {
		content, err := assets.Render(assets.ProviderTemplate(Name, workflowImport.template), data)
		if err != nil {
			return err
		}
		if err := plan.Write(
			filepath.Join(p.settings.OutputDir, workflowImport.file),
			[]byte(content),
			fspatch.KindSettings,
			false,
		); err != nil {
			return fmt.Errorf("failed to prepare %s: %w", workflowImport.file, err)
		}
	}

	return nil
}

func ghAWMCPServer(server provider.MCPServer) (MCPServer, error) {
	entry := MCPServer{
		Type:    server.Transport(),
		Allowed: []string{"*"},
	}
	if len(server.CWD) > 0 {
		return entry, fmt.Errorf("gh-aw does not support cwd")
	}

	switch server.Transport() {
	case provider.MCPServerTypeStdio:
		switch server.Command {
		case commandDocker, commandNPX, commandUVX:
		default:
			return entry, fmt.Errorf(
				"gh-aw cannot run this stdio command directly; configure a container or use docker, npx, uvx",
			)
		}
		entry.Command = server.Command
		entry.Args = slices.Clone(server.Args)
		entry.Env = make(map[string]string, len(server.Env))
		for name, value := range server.Env {
			if !ghAWEnvNamePattern.MatchString(name) {
				return entry, fmt.Errorf(
					"gh-aw requires environment variable names containing only uppercase letters, digits, or underscores",
				)
			}

			entry.Env[name] = value
		}
	case provider.MCPServerTypeHTTP:
		if len(server.Env) > 0 {
			return entry, fmt.Errorf("gh-aw does not support env for HTTP servers; use headers")
		}
		entry.URL = server.URL
		entry.Headers = make(map[string]string, len(server.Headers))
		for name, value := range server.Headers {
			if !ghAWHeaderPattern.MatchString(name) {
				return entry, fmt.Errorf(
					"gh-aw requires HTTP header names containing only letters, digits, underscores, or hyphens",
				)
			}

			entry.Headers[name] = value
		}
	default:
		return entry, fmt.Errorf("gh-aw supports only stdio and HTTP transports")
	}
	return entry, nil
}

func (p *Provider) resolveMCPBindings(server *MCPServer) error {
	values := []*string{
		&server.Container,
		&server.Version,
		&server.Entrypoint,
		&server.Command,
		&server.URL,
	}
	for _, args := range [][]string{server.EntrypointArgs, server.Mounts, server.Args} {
		for i := range args {
			values = append(values, &args[i])
		}
	}
	for _, value := range values {
		resolved, err := resolveBindingReferences(*value, p.settings.Bindings)
		if err != nil {
			return err
		}

		*value = resolved
	}
	for _, fields := range []map[string]string{server.Env, server.Headers} {
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			resolved, err := resolveBindingReferences(fields[name], p.settings.Bindings)
			if err != nil {
				return err
			}

			fields[name] = resolved
		}
	}
	return nil
}

// resolveBindingReferences substitutes declared bindings once without reading the local environment.
func resolveBindingReferences(value string, bindings map[string]string) (string, error) {
	var bindingErr error
	resolved := ghAWBindingPattern.ReplaceAllStringFunc(value, func(reference string) string {
		if strings.HasPrefix(reference, "${{") || bindingErr != nil {
			return reference
		}

		name := strings.Trim(strings.TrimPrefix(reference, "$"), "{}")
		binding, exists := bindings[name]
		if !exists || len(strings.TrimSpace(binding)) == 0 {
			bindingErr = fmt.Errorf("missing binding for %q; configure providers.gh-aw.bindings.%s", name, name)
			return reference
		}

		return binding
	})
	if bindingErr != nil {
		return "", bindingErr
	}

	return resolved, nil
}

// MCPToolName formats a server tool name for this provider.
func MCPToolName(_, toolName string) string {
	return toolName
}
