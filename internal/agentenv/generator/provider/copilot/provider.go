package copilot

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const Name = "copilot"

const fileMCP = ".mcp.json"

// MCPConfig represents the project configuration for Copilot CLI.
type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// MCPServer describes an MCP server in Copilot CLI's configuration.
// @see https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers
type MCPServer struct {
	Type    string            `json:"type"`              // stdio, http, sse
	URL     string            `json:"url,omitempty"`     // for sse and http
	Command string            `json:"command,omitempty"` //
	Args    []string          `json:"args,omitempty"`    //
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"` // for sse and http
	Tools   []string          `json:"tools"`             // ["*"] makes all server tools available
}

// Settings contains the inputs needed to generate this provider's files.
type Settings struct {
	OutputDir  string
	MCPServers map[string]provider.MCPServer
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
	mcpConfig := p.extractMCPServers()

	// Generate the project MCP configuration shared with Claude when enabled.
	cfg := MCPConfig{
		MCPServers: mcpConfig,
	}

	path := filepath.Join(p.settings.OutputDir, fileMCP)
	if err := provider.PrepareJSONFile(plan, path, cfg); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", path, err)
	}

	return nil
}

func (p *Provider) extractMCPServers() map[string]MCPServer {
	mcpServers := make(map[string]MCPServer)
	for name, server := range p.settings.MCPServers {
		if server.Enabled {
			mcpServers[name] = MCPServer{
				Type:    server.Transport(),
				URL:     server.URL,
				Tools:   []string{"*"},
				Headers: server.Headers,
				Command: server.Command,
				Args:    server.Args,
				CWD:     server.CWD,
				Env:     server.Env,
			}
		}
	}
	return mcpServers
}

// MCPToolName formats a server tool name for this provider.
func MCPToolName(serverName, toolName string) string {
	return serverName + "/" + toolName
}
