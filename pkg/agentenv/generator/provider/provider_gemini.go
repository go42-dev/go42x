package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const Gemini = "gemini"

const (
	geminiSettingsDir      = ".gemini"
	geminiSettingsFile     = "settings.json"
	mcpDefaultTimeout      = 30000 // in milliseconds
	mcpDefaultTrust        = true
	maxSessionsTurns       = 10
	checkpointingEnabled   = true
	usageStatisticsEnabled = false
)

// GeminiSettings represents .gemini/settings.json structure
type GeminiSettings struct {
	Tools struct {
		Core    []string `json:"core,omitempty"`
		Allowed []string `json:"allowed,omitempty"`
	} `json:"tools"`
	General struct {
		Checkpointing       GeminiCheckpointing `json:"checkpointing"`
		DefaultApprovalMode string              `json:"defaultApprovalMode"`
	} `json:"general"`
	Model struct {
		MaxSessionTurns int `json:"maxSessionTurns"`
	} `json:"model"`
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

// @see https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md
type GeminiMCPServerConfig struct {
	URL          string            `json:"url,omitempty"`     // for sse
	HttpUrl      string            `json:"httpUrl,omitempty"` // http streaming endpoint url
	Command      string            `json:"command,omitempty"` //
	Args         []string          `json:"args,omitempty"`    //
	Env          map[string]string `json:"env,omitempty"`     // $VAR_NAME or ${VAR_NAME} syntax
	CWD          string            `json:"cwd,omitempty"`     // current working directory
	Timeout      int               `json:"timeout,omitempty"` //
	Trust        bool              `json:"trust,omitempty"`   //
	Headers      map[string]string `json:"headers,omitempty"` // when using url or httpUrl
	IncludeTools []string          `json:"includeTools,omitempty"`
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

func (p *GeminiProvider) Generate(ctxData map[string]interface{}, providerConfig config.Provider) error {
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

func (p *GeminiProvider) generateConfigFiles(providerConfig config.Provider) error {
	enabledServers, mcpServers := p.extractMCPServers()

	// Generate .gemini/settings.json
	geminiSettings := GeminiSettings{
		MCPServers: mcpServers,
	}
	geminiSettings.Tools.Core = providerConfig.Tools
	geminiSettings.Tools.Allowed = providerConfig.Tools
	geminiSettings.General.Checkpointing.Enabled = checkpointingEnabled
	geminiSettings.General.DefaultApprovalMode = "auto_edit"
	geminiSettings.Model.MaxSessionTurns = maxSessionsTurns
	geminiSettings.MCP.Allowed = enabledServers
	geminiSettings.Privacy.UsageStatisticsEnabled = usageStatisticsEnabled

	geminiDir := filepath.Join(p.outputDir, geminiSettingsDir)
	settingsPath := filepath.Join(geminiDir, geminiSettingsFile)
	if err := p.writeJSONFile(settingsPath, geminiSettings); err != nil {
		return fmt.Errorf("failed to write %s: %w", settingsPath, err)
	}

	p.logger.Info("Generated output", "file", settingsPath)

	return nil
}

func (p *GeminiProvider) extractMCPServers() ([]string, map[string]GeminiMCPServerConfig) {
	enabledServers := []string{}
	mcpServers := make(map[string]GeminiMCPServerConfig)

	for name, server := range p.config.MCP {
		if server.Enabled {
			enabledServers = append(enabledServers, name)
			mcpServer := GeminiMCPServerConfig{
				Command:      server.Command,
				Args:         server.Args,
				Env:          server.Env,
				Timeout:      mcpDefaultTimeout,
				Trust:        mcpDefaultTrust,
				Headers:      server.Headers,
				CWD:          server.CWD,
				IncludeTools: server.Tools,
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

func (p *GeminiProvider) writeJSONFile(path string, data interface{}) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return p.writeOutput(path, string(content))
}
