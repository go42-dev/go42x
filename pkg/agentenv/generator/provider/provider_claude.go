package provider

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

const (
	Claude             = "claude"
	claudeSettingsDir  = ".claude"
	claudeSettingsFile = "settings.local.json"
	claudeMCPFile      = ".mcp.json"
	claudeAgentsDir    = "agents"
)

// ClaudeSettings represents .claude/settings.local.json structure
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

func (p *ClaudeProvider) Prepare(
	plan *output.Plan,
	ctxData map[string]interface{},
	providerConfig config.Provider,
) error {
	if err := p.prepareConfigFiles(plan, providerConfig); err != nil {
		return fmt.Errorf("failed to prepare config files: %w", err)
	}

	if err := p.prepareAgents(plan, providerConfig, ctxData); err != nil {
		return fmt.Errorf("failed to prepare agents: %w", err)
	}

	return nil
}

func (p *ClaudeProvider) prepareConfigFiles(plan *output.Plan, providerConfig config.Provider) error {
	allTools := p.collectAllTools(providerConfig)
	enabledServers, mcpServers := p.extractMCPServers(&allTools)

	// Generate .claude/settings.local.json
	claudeSettings := ClaudeSettings{}
	claudeSettings.Permissions.Allow = allTools
	claudeSettings.Permissions.Deny = []string{}
	claudeSettings.EnabledMCPServers = enabledServers

	claudeDir := filepath.Join(p.outputDir, claudeSettingsDir)
	settingsPath := filepath.Join(claudeDir, claudeSettingsFile)
	if err := p.prepareJSONSettings(plan, settingsPath, claudeSettings,
		[]string{"permissions.allow", "enabledMcpjsonServers"},
		[]string{"permissions.deny"},
	); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", settingsPath, err)
	}

	// Copilot owns the shared file when enabled.
	if p.config.ProviderEnabled(Copilot) {
		return nil
	}

	// Generate .mcp.json
	mcpConfig := ClaudeMCPConfig{
		MCPServers: mcpServers,
	}

	mcpPath := filepath.Join(p.outputDir, claudeMCPFile)
	if err := p.prepareJSONFile(plan, mcpPath, mcpConfig); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", mcpPath, err)
	}

	return nil
}

func (p *ClaudeProvider) collectAllTools(providerConfig config.Provider) []string {
	allTools := make([]string, 0, len(providerConfig.AutoApproveTools))
	allTools = append(allTools, providerConfig.AutoApproveTools...)
	return allTools
}

func (p *ClaudeProvider) extractMCPServers(allTools *[]string) ([]string, map[string]ClaudeMCPServer) {
	enabledServers := make([]string, 0)
	mcpServers := make(map[string]ClaudeMCPServer)
	for name, server := range p.config.MCP {
		if server.Enabled {
			enabledServers = append(enabledServers, name)
			for _, tool := range server.Tools {
				*allTools = append(*allTools, MCPToolName(Claude, name, tool))
			}
			mcpServers[name] = ClaudeMCPServer{
				Command: server.Command,
				Args:    server.Args,
				Env:     server.Env,
				Type:    server.Transport(),
				URL:     server.URL,
				Headers: server.Headers,
			}
		}
	}
	sort.Strings(enabledServers)
	sort.Strings(*allTools)
	return enabledServers, mcpServers
}

func (p *ClaudeProvider) prepareAgents(
	plan *output.Plan,
	providerConfig config.Provider,
	ctxData map[string]interface{},
) error {
	if len(providerConfig.Agents) == 0 {
		return nil
	}

	destAgentsDir := filepath.Join(p.outputDir, claudeSettingsDir, claudeAgentsDir)

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

		// Prepare destination: {outputDir}/.claude/agents/{agent}.md
		destFile := fmt.Sprintf("%s.md", agentName)
		destPath := filepath.Join(destAgentsDir, destFile)
		if err := plan.Write(destPath, []byte(processedContent), output.Settings, false); err != nil {
			return fmt.Errorf("failed to prepare agent %s: %w", agentName, err)
		}

		p.logger.Debug("Prepared agent", "source", agentPath, "name", agentName, "dest", destPath)
	}

	return nil
}
