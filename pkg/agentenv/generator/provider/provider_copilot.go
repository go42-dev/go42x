package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const Copilot = "copilot"

const (
	copilotMcpConfigDir  = ".github"
	copilotMcpConfigFile = ".copilot.mcp.json"
)

// ClaudeMCPConfig represents .mcp.json structure
type CopilotMCPConfig struct {
	MCPServers map[string]CopilotMCPServer `json:"mcpServers"`
}

// @see https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/extend-coding-agent-with-mcp
type CopilotMCPServer struct {
	Type    string            `json:"type"`              // local, http, sse
	URL     string            `json:"url,omitempty"`     // for sse and http
	Command string            `json:"command,omitempty"` //
	Args    []string          `json:"args,omitempty"`    //
	Env     map[string]string `json:"env,omitempty"`     // gh secret `COPILOT_MCP_`
	Headers map[string]string `json:"headers,omitempty"` // for sse and http, gh secret `$COPILOT_MCP_`
	Tools   []string          `json:"tools,omitempty"`   // list of allowed tools, required for local type
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

func (p *CopilotProvider) Generate(ctxData map[string]interface{}, providerConfig config.Provider) error {
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

func (p *CopilotProvider) generateConfigFiles(providerConfig config.Provider) error {
	allTools := p.collectAllTools(providerConfig)
	mcpConfig := p.extractMCPServers(&allTools)

	// Generate .copilot.mcp.json
	cfg := CopilotMCPConfig{
		MCPServers: mcpConfig,
	}

	path := filepath.Join(p.outputDir, copilotMcpConfigDir, copilotMcpConfigFile)
	if err := p.writeJSONFile(path, cfg); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	p.logger.Info("Generated output", "file", path)

	return nil
}

func (p *CopilotProvider) collectAllTools(providerConfig config.Provider) []string {
	allTools := make([]string, 0, len(providerConfig.Tools))
	allTools = append(allTools, providerConfig.Tools...)
	return allTools
}

func (p *CopilotProvider) extractMCPServers(allTools *[]string) map[string]CopilotMCPServer {
	mcpServers := make(map[string]CopilotMCPServer)
	for name, server := range p.config.MCP {
		if server.Enabled {
			*allTools = append(*allTools, server.Tools...)
			mcpServers[name] = CopilotMCPServer{
				Type:    server.Type,
				URL:     server.URL,
				Tools:   server.Tools,
				Headers: server.Headers,
				Command: server.Command,
				Args:    server.Args,
				Env:     server.Env,
			}
		}
	}
	return mcpServers
}

func (p *CopilotProvider) writeJSONFile(path string, data interface{}) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return p.writeOutput(path, string(content))
}
