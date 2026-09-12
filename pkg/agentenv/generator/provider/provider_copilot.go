package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const Copilot = "copilot"

const (
	copilotMcpConfigDir  = ".github"
	copilotMcpConfigFile = ".copilot.mcp.json"
)

// CopilotMCPConfig represents the configuration to import into Copilot cloud agent settings.
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
	Tools   []string          `json:"tools"`             // raw tool names, required for all transports
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

	if err := p.generateConfigFiles(); err != nil {
		return fmt.Errorf("failed to generate config files: %w", err)
	}

	return nil
}

func (p *CopilotProvider) generateConfigFiles() error {
	mcpConfig := p.extractMCPServers()

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

func (p *CopilotProvider) extractMCPServers() map[string]CopilotMCPServer {
	mcpServers := make(map[string]CopilotMCPServer)
	for name, server := range p.config.MCP {
		if server.Enabled {
			args := make([]string, len(server.Args))
			for i, arg := range server.Args {
				args[i] = copilotSecretReferences(arg)
			}
			mcpServers[name] = CopilotMCPServer{
				Type:    server.Transport(),
				URL:     copilotSecretReferences(server.URL),
				Tools:   append([]string{}, server.Tools...),
				Headers: copilotSecretMap(server.Headers),
				Command: copilotSecretReferences(server.Command),
				Args:    args,
				Env:     copilotSecretMap(server.Env),
			}
		}
	}
	return mcpServers
}

var envReferencePattern = regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_]*`)

// Keep secret references in the generated file; never resolve local credentials.
func copilotSecretReferences(value string) string {
	return envReferencePattern.ReplaceAllStringFunc(value, func(reference string) string {
		prefix := "$"
		if strings.HasPrefix(reference, "${") {
			prefix = "${"
		}
		name := strings.TrimPrefix(reference, prefix)
		if strings.HasPrefix(name, "COPILOT_MCP_") {
			return reference
		}
		return prefix + "COPILOT_MCP_" + name
	})
}

func copilotSecretMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = copilotSecretReferences(value)
	}
	return result
}

func (p *CopilotProvider) writeJSONFile(path string, data interface{}) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return p.writeOutput(path, string(content))
}
