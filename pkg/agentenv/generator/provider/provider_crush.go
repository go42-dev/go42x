package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
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

func (p *CrushProvider) Generate(ctxData map[string]interface{}, providerConfig config.Provider) error {
	templateContent, err := p.loadTemplate(providerConfig.Template)
	if err != nil {
		return fmt.Errorf("failed to load template: %w", err)
	}

	if len(providerConfig.Chunks) > 0 {
		chunkContents, err := p.loadTemplates(providerConfig.Chunks)
		if err != nil {
			return fmt.Errorf("failed to load chunks: %w", err)
		}

		mergedChunks := p.mergeStrings(chunkContents)
		templateContent = p.templateEngine.InjectChunks(templateContent, mergedChunks)
	}

	if len(providerConfig.Modes) > 0 {
		modeContents, err := p.loadTemplates(providerConfig.Modes)
		if err != nil {
			return fmt.Errorf("failed to load modes: %w", err)
		}

		mergedModes := p.mergeStrings(modeContents)
		templateContent = p.templateEngine.InjectModes(templateContent, mergedModes)
	}

	if len(providerConfig.Workflows) > 0 {
		workflowContents, err := p.loadTemplates(providerConfig.Workflows)
		if err != nil {
			return fmt.Errorf("failed to load workflows: %w", err)
		}

		mergedWorkflows := p.mergeStrings(workflowContents)
		templateContent = p.templateEngine.InjectWorkflows(templateContent, mergedWorkflows)
	}

	output, err := p.templateEngine.Process(templateContent, ctxData)
	if err != nil {
		return fmt.Errorf("failed to process template: %w", err)
	}

	outputPath := filepath.Join(p.outputDir, providerConfig.Output)
	if err := p.writeOutput(outputPath, output); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	p.logger.Info("Generated output", "file", outputPath)

	if err := p.generateConfigFiles(providerConfig); err != nil {
		return fmt.Errorf("failed to generate config files: %w", err)
	}

	return nil
}

func (p *CrushProvider) generateConfigFiles(providerConfig config.Provider) error {
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
	if err := p.writeJSONFile(crushPath, crushConfig); err != nil {
		return fmt.Errorf("failed to write %s: %w", crushPath, err)
	}

	p.logger.Info("Generated output", "file", crushPath)

	return nil
}

func (p *CrushProvider) collectAllTools(providerConfig config.Provider) []string {
	allTools := make([]string, 0, len(providerConfig.Tools))
	allTools = append(allTools, providerConfig.Tools...)
	return allTools
}

func (p *CrushProvider) extractMCPServers(allTools *[]string) map[string]CrushMCPConfig {
	mcpServers := make(map[string]CrushMCPConfig)

	for name, server := range p.config.MCP {
		// crush has built-in support for gopls, adding it again as mcp server causes issues
		if server.Enabled && server.Command != "gopls" {
			*allTools = append(*allTools, server.Tools...)
			mcpServers[name] = CrushMCPConfig{
				Type:    server.Type,
				Command: server.Command,
				Args:    server.Args,
				Env:     server.Env,
				URL:     server.URL,
				Headers: server.Headers,
			}
		}
	}

	return mcpServers
}

func (p *CrushProvider) writeJSONFile(path string, data interface{}) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return p.writeOutput(path, string(content))
}
