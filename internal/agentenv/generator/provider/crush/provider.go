package crush

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const Name = "crush"

const schemaURL = "https://charm.land/crush.json"

const fileConfig = ".crush.json"

// Config represents the .crush.json structure.
type Config struct {
	Schema      string               `json:"$schema"`
	LSP         map[string]LSPConfig `json:"lsp"`
	MCP         map[string]MCPConfig `json:"mcp"`
	Permissions Permissions          `json:"permissions"`
}

type LSPConfig struct {
	Command string `json:"command"`
}

// MCPConfig describes an MCP server in Crush's configuration.
// @see https://github.com/charmbracelet/crush?tab=readme-ov-file#mcps
type MCPConfig struct {
	Type    string            `json:"type"`              // stdio, http, sse
	URL     string            `json:"url,omitempty"`     // for sse and http
	Command string            `json:"command,omitempty"` //
	Args    []string          `json:"args,omitempty"`    //
	Env     map[string]string `json:"env,omitempty"`     //
	Headers map[string]string `json:"headers,omitempty"` // when using sse and http
}

type Permissions struct {
	AllowedTools []string `json:"allowed_tools"`
}

// Settings contains the inputs needed to generate this provider's files.
type Settings struct {
	OutputDir        string
	MCPServers       map[string]provider.MCPServer
	AutoApproveTools []string
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
	allTools := p.collectAllTools()
	mcpConfig := p.extractMCPServers(&allTools)

	// Generate .crush.json
	crushConfig := Config{
		Schema:      schemaURL,
		LSP:         map[string]LSPConfig{"go": {Command: "gopls"}},
		MCP:         mcpConfig,
		Permissions: Permissions{AllowedTools: allTools},
	}

	crushPath := filepath.Join(p.settings.OutputDir, fileConfig)
	if err := provider.PrepareJSONSettings(plan, crushPath, crushConfig,
		[]string{"mcp", "permissions.allowed_tools"},
		[]string{"$schema", "lsp.go"},
	); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", crushPath, err)
	}

	return nil
}

func (p *Provider) collectAllTools() []string {
	allTools := make([]string, 0, len(p.settings.AutoApproveTools))
	allTools = append(allTools, p.settings.AutoApproveTools...)
	return allTools
}

func (p *Provider) extractMCPServers(allTools *[]string) map[string]MCPConfig {
	mcpServers := make(map[string]MCPConfig)

	for name, server := range p.settings.MCPServers {
		if !server.Enabled {
			continue
		}

		for _, tool := range server.Tools {
			*allTools = append(*allTools, MCPToolName(name, tool))
		}
		mcpServers[name] = MCPConfig{
			Type:    server.Transport(),
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
			URL:     server.URL,
			Headers: server.Headers,
		}
	}

	sort.Strings(*allTools)
	return mcpServers
}

// MCPToolName formats a server tool name for this provider.
func MCPToolName(serverName, toolName string) string {
	return fmt.Sprintf("mcp_%s_%s", serverName, toolName)
}
