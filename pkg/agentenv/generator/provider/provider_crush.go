package provider

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

const Crush = "crush"

const (
	crushSchema     = "https://charm.land/crush.json"
	crushConfigFile = ".crush.json"
)

// CrushConfig represents .crush.json structure
type CrushConfig struct {
	Schema      string                    `json:"$schema"`
	LSP         map[string]LSPConfig      `json:"lsp"`
	MCP         map[string]CrushMCPConfig `json:"mcp"`
	Permissions CrushPermissions          `json:"permissions"`
}

type LSPConfig struct {
	Command string `json:"command"`
}

// @see https://github.com/charmbracelet/crush?tab=readme-ov-file#mcps
type CrushMCPConfig struct {
	Type    string            `json:"type"`              // stdio, http, sse
	URL     string            `json:"url,omitempty"`     // for sse and http
	Command string            `json:"command,omitempty"` //
	Args    []string          `json:"args,omitempty"`    //
	Env     map[string]string `json:"env,omitempty"`     //
	Headers map[string]string `json:"headers,omitempty"` // when using sse and http
}

type CrushPermissions struct {
	AllowedTools []string `json:"allowed_tools"`
}

type CrushProvider struct {
	*BaseProvider
}

func NewCrushProvider(
	logger *slog.Logger,
	cfg *config.Config,
	templateEngine TemplateEngineAccessor,
	templateDir, outputDir string,
) *CrushProvider {
	return &CrushProvider{
		BaseProvider: NewBaseProvider(logger, cfg, templateEngine, templateDir, outputDir),
	}
}

func (p *CrushProvider) Prepare(plan *output.Plan, _ map[string]interface{}, providerConfig config.Provider) error {
	if err := p.prepareConfigFiles(plan, providerConfig); err != nil {
		return fmt.Errorf("failed to prepare config files: %w", err)
	}

	return nil
}

func (p *CrushProvider) prepareConfigFiles(plan *output.Plan, providerConfig config.Provider) error {
	allTools := p.collectAllTools(providerConfig)
	mcpConfig := p.extractMCPServers(&allTools)

	// Generate .crush.json
	crushConfig := CrushConfig{
		Schema:      crushSchema,
		LSP:         map[string]LSPConfig{"go": {Command: "gopls"}},
		MCP:         mcpConfig,
		Permissions: CrushPermissions{AllowedTools: allTools},
	}

	crushPath := filepath.Join(p.outputDir, crushConfigFile)
	if err := p.prepareJSONSettings(plan, crushPath, crushConfig,
		[]string{"mcp", "permissions.allowed_tools"},
		[]string{"$schema", "lsp.go"},
	); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", crushPath, err)
	}

	return nil
}

func (p *CrushProvider) collectAllTools(providerConfig config.Provider) []string {
	allTools := make([]string, 0, len(providerConfig.AutoApproveTools))
	allTools = append(allTools, providerConfig.AutoApproveTools...)
	return allTools
}

func (p *CrushProvider) extractMCPServers(allTools *[]string) map[string]CrushMCPConfig {
	mcpServers := make(map[string]CrushMCPConfig)

	for name, server := range p.config.MCP {
		if !server.Enabled {
			continue
		}
		for _, tool := range server.Tools {
			*allTools = append(*allTools, MCPToolName(Crush, name, tool))
		}
		mcpServers[name] = CrushMCPConfig{
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
