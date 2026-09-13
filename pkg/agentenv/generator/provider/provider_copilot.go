package provider

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

const Copilot = "copilot"

const copilotMcpConfigFile = ".mcp.json"

// CopilotMCPConfig represents the project configuration for Copilot CLI.
type CopilotMCPConfig struct {
	MCPServers map[string]CopilotMCPServer `json:"mcpServers"`
}

// @see https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers
type CopilotMCPServer struct {
	Type    string            `json:"type"`              // stdio, http, sse
	URL     string            `json:"url,omitempty"`     // for sse and http
	Command string            `json:"command,omitempty"` //
	Args    []string          `json:"args,omitempty"`    //
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"` // for sse and http
	Tools   []string          `json:"tools"`             // ["*"] makes all server tools available
}

type CopilotProvider struct {
	*BaseProvider
}

func NewCopilotProvider(
	logger *slog.Logger,
	cfg *config.Config,
	templateEngine TemplateEngineAccessor,
	templateDir, outputDir string,
) *CopilotProvider {
	return &CopilotProvider{
		BaseProvider: NewBaseProvider(logger, cfg, templateEngine, templateDir, outputDir),
	}
}

func (p *CopilotProvider) Prepare(plan *output.Plan, _ map[string]interface{}, _ config.Provider) error {
	if err := p.prepareConfigFiles(plan); err != nil {
		return fmt.Errorf("failed to prepare config files: %w", err)
	}

	return nil
}

func (p *CopilotProvider) prepareConfigFiles(plan *output.Plan) error {
	mcpConfig := p.extractMCPServers()

	// Generate the project MCP configuration shared with Claude when enabled.
	cfg := CopilotMCPConfig{
		MCPServers: mcpConfig,
	}

	path := filepath.Join(p.outputDir, copilotMcpConfigFile)
	if err := p.prepareJSONFile(plan, path, cfg); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", path, err)
	}

	return nil
}

func (p *CopilotProvider) extractMCPServers() map[string]CopilotMCPServer {
	mcpServers := make(map[string]CopilotMCPServer)
	for name, server := range p.config.MCP {
		if server.Enabled {
			mcpServers[name] = CopilotMCPServer{
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
