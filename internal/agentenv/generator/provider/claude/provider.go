package claude

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const (
	Name               = "claude"
	claudeSettingsDir  = ".claude"
	claudeSettingsFile = "settings.local.json"
	claudeMCPFile      = ".mcp.json"
	claudeAgentsDir    = "agents"
)

// Permissions contains the generated tool approval lists.
type Permissions struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// LocalSettings represents .claude/settings.local.json.
type LocalSettings struct {
	Permissions       Permissions `json:"permissions"`
	EnabledMCPServers []string    `json:"enabledMcpjsonServers"`
}

// MCPConfig represents the .mcp.json structure.
type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// MCPServer describes an MCP server in Claude's configuration.
// @see https://docs.anthropic.com/en/docs/claude-code/mcp
type MCPServer struct {
	Command string            `json:"command,omitempty"` //
	URL     string            `json:"url,omitempty"`     // for sse
	Type    string            `json:"type"`              // sse / http
	Args    []string          `json:"args,omitempty"`    //
	Env     map[string]string `json:"env,omitempty"`     //
	Headers map[string]string `json:"headers,omitempty"` // Authorization: Bearer ...
}

// Settings contains the inputs needed to generate this provider's files.
type Settings struct {
	OutputDir        string
	MCPServers       map[string]provider.MCPServer
	Agents           []string
	AutoApproveTools []string
	WriteMCPFile     bool
}

type Provider struct {
	logger         *slog.Logger
	templateEngine provider.TemplateEngineAccessor
	settings       Settings
	templateDir    string
}

func New(
	logger *slog.Logger,
	templateEngine provider.TemplateEngineAccessor,
	settings Settings,
	templateDir string,
) *Provider {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Provider{
		logger:         logger,
		templateEngine: templateEngine,
		settings:       settings,
		templateDir:    templateDir,
	}
}

func (p *Provider) Prepare(
	plan *fspatch.Plan,
	ctxData map[string]any,
) error {
	if err := p.prepareConfigFiles(plan); err != nil {
		return fmt.Errorf("failed to prepare config files: %w", err)
	}

	if err := p.prepareAgents(plan, ctxData); err != nil {
		return fmt.Errorf("failed to prepare agents: %w", err)
	}

	return nil
}

func (p *Provider) prepareConfigFiles(plan *fspatch.Plan) error {
	allTools := p.collectAllTools()
	enabledServers, mcpServers := p.extractMCPServers(&allTools)

	// Generate .claude/settings.local.json
	claudeSettings := LocalSettings{}
	claudeSettings.Permissions.Allow = allTools
	claudeSettings.Permissions.Deny = []string{}
	claudeSettings.EnabledMCPServers = enabledServers

	claudeDir := filepath.Join(p.settings.OutputDir, claudeSettingsDir)
	settingsPath := filepath.Join(claudeDir, claudeSettingsFile)
	if err := provider.PrepareJSONSettings(plan, settingsPath, claudeSettings,
		[]string{"permissions.allow", "enabledMcpjsonServers"},
		[]string{"permissions.deny"},
	); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", settingsPath, err)
	}

	// The generator assigns ownership of the shared MCP file.
	if !p.settings.WriteMCPFile {
		return nil
	}

	// Generate .mcp.json
	mcpConfig := MCPConfig{
		MCPServers: mcpServers,
	}

	mcpPath := filepath.Join(p.settings.OutputDir, claudeMCPFile)
	if err := provider.PrepareJSONFile(plan, mcpPath, mcpConfig); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", mcpPath, err)
	}

	return nil
}

func (p *Provider) collectAllTools() []string {
	allTools := make([]string, 0, len(p.settings.AutoApproveTools))
	allTools = append(allTools, p.settings.AutoApproveTools...)
	return allTools
}

func (p *Provider) extractMCPServers(allTools *[]string) ([]string, map[string]MCPServer) {
	enabledServers := make([]string, 0)
	mcpServers := make(map[string]MCPServer)
	for name, server := range p.settings.MCPServers {
		if server.Enabled {
			enabledServers = append(enabledServers, name)
			for _, tool := range server.Tools {
				*allTools = append(*allTools, MCPToolName(name, tool))
			}
			mcpServers[name] = MCPServer{
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

func (p *Provider) prepareAgents(
	plan *fspatch.Plan,
	ctxData map[string]any,
) error {
	if len(p.settings.Agents) == 0 {
		return nil
	}

	destAgentsDir := filepath.Join(p.settings.OutputDir, claudeSettingsDir, claudeAgentsDir)

	for _, agentPath := range p.settings.Agents {
		// Extract just the filename without .tpl.md extension for the agent name
		baseName := filepath.Base(agentPath)
		agentName := strings.TrimSuffix(baseName, ".tpl.md")

		// Read the template file from templateDir
		sourcePath := filepath.Join(p.templateDir, agentPath)
		// #nosec G304 -- Agent template paths are selected by the trusted local project configuration.
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
		if err := plan.Write(destPath, []byte(processedContent), fspatch.Settings, false); err != nil {
			return fmt.Errorf("failed to prepare agent %s: %w", agentName, err)
		}

		p.logger.Debug(
			"Prepared agent",
			slog.String("source", agentPath),
			slog.String("name", agentName),
			slog.String("dest", destPath),
		)
	}

	return nil
}

// MCPToolName formats a server tool name for this provider.
func MCPToolName(serverName, toolName string) string {
	return fmt.Sprintf("mcp__%s__%s", serverName, toolName)
}
