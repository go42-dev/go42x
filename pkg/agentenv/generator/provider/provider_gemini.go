package provider

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

const (
	Gemini                 = "gemini"
	geminiSettingsDir      = ".gemini"
	geminiSettingsFile     = "settings.json"
	mcpDefaultTimeout      = 30000 // in milliseconds
	mcpDefaultTrust        = true
	checkpointingEnabled   = true
	usageStatisticsEnabled = false
)

// GeminiSettings represents .gemini/settings.json structure
type GeminiSettings struct {
	Tools struct {
		Allowed []string `json:"allowed,omitempty"`
	} `json:"tools"`
	General struct {
		Checkpointing       GeminiCheckpointing `json:"checkpointing"`
		DefaultApprovalMode string              `json:"defaultApprovalMode"`
	} `json:"general"`
	MCP struct {
		Allowed []string `json:"allowed"`
	} `json:"mcp"`
	Privacy struct {
		UsageStatisticsEnabled bool `json:"usageStatisticsEnabled"`
	} `json:"privacy"`
	MCPServers map[string]GeminiMCPServerConfig `json:"mcpServers"`
}

type GeminiCheckpointing struct {
	Enabled bool `json:"enabled"`
}

// GeminiMCPServerConfig
// @see https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md
type GeminiMCPServerConfig struct {
	URL     string            `json:"url,omitempty"`     // for sse
	HttpUrl string            `json:"httpUrl,omitempty"` // http streaming endpoint url
	Command string            `json:"command,omitempty"` //
	Args    []string          `json:"args,omitempty"`    //
	Env     map[string]string `json:"env,omitempty"`     // $VAR_NAME or ${VAR_NAME} syntax
	CWD     string            `json:"cwd,omitempty"`     // current working directory
	Timeout int               `json:"timeout,omitempty"` //
	Trust   bool              `json:"trust,omitempty"`   //
	Headers map[string]string `json:"headers,omitempty"` // when using url or httpUrl
}

type GeminiProvider struct {
	*BaseProvider
}

func NewGeminiProvider(
	logger *slog.Logger,
	cfg *config.Config,
	templateEngine TemplateEngineAccessor,
	templateDir, outputDir string,
) *GeminiProvider {
	return &GeminiProvider{
		BaseProvider: NewBaseProvider(logger, cfg, templateEngine, templateDir, outputDir),
	}
}

func (p *GeminiProvider) Prepare(plan *output.Plan, _ map[string]interface{}, providerConfig config.Provider) error {
	if err := p.prepareConfigFiles(plan, providerConfig); err != nil {
		return fmt.Errorf("failed to prepare config files: %w", err)
	}

	return nil
}

func (p *GeminiProvider) prepareConfigFiles(plan *output.Plan, providerConfig config.Provider) error {
	enabledServers, mcpServers := p.extractMCPServers()

	// Generate .gemini/settings.json
	geminiSettings := GeminiSettings{
		MCPServers: mcpServers,
	}
	geminiSettings.Tools.Allowed = providerConfig.AutoApproveTools
	geminiSettings.General.Checkpointing.Enabled = checkpointingEnabled
	geminiSettings.General.DefaultApprovalMode = "auto_edit"
	geminiSettings.MCP.Allowed = enabledServers
	geminiSettings.Privacy.UsageStatisticsEnabled = usageStatisticsEnabled

	geminiDir := filepath.Join(p.outputDir, geminiSettingsDir)
	settingsPath := filepath.Join(geminiDir, geminiSettingsFile)
	// Remove the built-in tool filter written by earlier versions.
	if err := p.prepareJSONSettings(plan, settingsPath, geminiSettings,
		[]string{"mcpServers", "mcp.allowed", "tools.core", "tools.allowed"},
		[]string{
			"general.checkpointing.enabled", "general.defaultApprovalMode",
			"privacy.usageStatisticsEnabled",
		},
	); err != nil {
		return fmt.Errorf("failed to prepare %s: %w", settingsPath, err)
	}

	return nil
}

func (p *GeminiProvider) extractMCPServers() ([]string, map[string]GeminiMCPServerConfig) {
	enabledServers := []string{}
	mcpServers := make(map[string]GeminiMCPServerConfig)

	for name, server := range p.config.MCP {
		if server.Enabled {
			enabledServers = append(enabledServers, name)
			mcpServer := GeminiMCPServerConfig{
				Command: server.Command,
				Args:    server.Args,
				Env:     server.Env,
				Timeout: mcpDefaultTimeout,
				Trust:   mcpDefaultTrust,
				Headers: server.Headers,
				CWD:     server.CWD,
			}
			if server.Transport() == config.MCPServerTypeHTTP {
				mcpServer.HttpUrl = server.URL
			} else if server.Transport() == config.MCPServerTypeSSE {
				mcpServer.URL = server.URL
			}
			mcpServers[name] = mcpServer
		}
	}

	sort.Strings(enabledServers)
	return enabledServers, mcpServers
}
