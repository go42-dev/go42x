package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const Claude = "claude"

const (
	claudeSettingsDir  = ".claude"
	claudeSettingsFile = "settings.json"
	claudeMCPFile      = ".mcp.json"
	claudeAgentsDir    = "agents"
)

// ClaudeSettings represents .claude/settings.json structure
type ClaudeSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
	EnabledMCPServers []string `json:"enabledMcpjsonServers"`
}

// ClaudeMCPConfig represents .mcp.json structure
type ClaudeMCPConfig struct {
	MCPServers map[string]ClaudeMCPServer `json:"mcpServers"`
}

// @see https://docs.anthropic.com/en/docs/claude-code/mcp
type ClaudeMCPServer struct {
	Command string            `json:"command,omitempty"` //
	URL     string            `json:"url,omitempty"`     // for sse
	Type    string            `json:"type"`              // sse / http
	Args    []string          `json:"args,omitempty"`    //
	Env     map[string]string `json:"env,omitempty"`     //
	Headers map[string]string `json:"headers,omitempty"` // Authorization: Bearer ...
}

type ClaudeProvider struct {
	*BaseProvider
}

func NewClaudeProvider(
	logger *slog.Logger,
	cfg *config.Config,
	templateEngine TemplateEngineAccessor,
	templateDir, outputDir string,
) *ClaudeProvider {
	return &ClaudeProvider{
		BaseProvider: NewBaseProvider(logger, cfg, templateEngine, templateDir, outputDir),
	}
}

func (p *ClaudeProvider) Generate(ctxData map[string]interface{}, providerConfig config.Provider) error {
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

	if err := p.copyAgents(providerConfig, ctxData); err != nil {
		return fmt.Errorf("failed to copy agents: %w", err)
	}

	return nil
}

func (p *ClaudeProvider) generateConfigFiles(providerConfig config.Provider) error {
	allTools := p.collectAllTools(providerConfig)
	enabledServers, mcpServers := p.extractMCPServers(&allTools)

	// Generate .claude/settings.json
	claudeSettings := ClaudeSettings{}
	claudeSettings.Permissions.Allow = allTools
	claudeSettings.Permissions.Deny = []string{}
	claudeSettings.EnabledMCPServers = enabledServers

	claudeDir := filepath.Join(p.outputDir, claudeSettingsDir)
	settingsPath := filepath.Join(claudeDir, claudeSettingsFile)
	if err := p.writeJSONFile(settingsPath, claudeSettings); err != nil {
		return fmt.Errorf("failed to write %s: %w", settingsPath, err)
	}

	p.logger.Info("Generated output", "file", settingsPath)

	// Generate .mcp.json
	mcpConfig := ClaudeMCPConfig{
		MCPServers: mcpServers,
	}

	mcpPath := filepath.Join(p.outputDir, claudeMCPFile)
	if err := p.writeJSONFile(mcpPath, mcpConfig); err != nil {
		return fmt.Errorf("failed to write %s: %w", mcpPath, err)
	}

	p.logger.Info("Generated output", "file", mcpPath)

	return nil
}

func (p *ClaudeProvider) collectAllTools(providerConfig config.Provider) []string {
	allTools := make([]string, 0, len(providerConfig.Tools))
	allTools = append(allTools, providerConfig.Tools...)
	return allTools
}

func (p *ClaudeProvider) extractMCPServers(allTools *[]string) ([]string, map[string]ClaudeMCPServer) {
	enabledServers := make([]string, 0)
	mcpServers := make(map[string]ClaudeMCPServer)
	for name, server := range p.config.MCP {
		if server.Enabled {
			enabledServers = append(enabledServers, name)
			*allTools = append(*allTools, server.Tools...)
			mcpServers[name] = ClaudeMCPServer{
				Command: server.Command,
				Args:    server.Args,
				Env:     server.Env,
				Type:    server.Type,
				URL:     server.URL,
				Headers: server.Headers,
			}
		}
	}
	return enabledServers, mcpServers
}

func (p *ClaudeProvider) writeJSONFile(path string, data interface{}) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return p.writeOutput(path, string(content))
}

func (p *ClaudeProvider) copyAgents(providerConfig config.Provider, ctxData map[string]interface{}) error {
	if len(providerConfig.Agents) == 0 {
		return nil
	}

	// Create destination directory: {outputDir}/.claude/agents
	destAgentsDir := filepath.Join(p.outputDir, claudeSettingsDir, claudeAgentsDir)
	if err := os.MkdirAll(destAgentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create agents directory: %w", err)
	}

	for _, agentPath := range providerConfig.Agents {
		// Extract just the filename without .tpl.md extension for the agent name
		baseName := filepath.Base(agentPath)
		agentName := strings.TrimSuffix(baseName, ".tpl.md")

		// Read the template file from templateDir
		sourcePath := filepath.Join(p.templateDir, agentPath)
		templateContent, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to read agent template %s: %w", agentPath, err)
		}

		// Process as template with context
		processedContent, err := p.templateEngine.Process(string(templateContent), ctxData)
		if err != nil {
			return fmt.Errorf("failed to process agent template %s: %w", agentPath, err)
		}

		// Write to destination: {outputDir}/.claude/agents/{agent}.md
		destFile := fmt.Sprintf("%s.md", agentName)
		destPath := filepath.Join(destAgentsDir, destFile)
		if err := os.WriteFile(destPath, []byte(processedContent), 0644); err != nil {
			return fmt.Errorf("failed to write agent %s: %w", agentName, err)
		}

		p.logger.Info("Processed agent", "source", agentPath, "name", agentName, "dest", destPath)
	}

	return nil
}
