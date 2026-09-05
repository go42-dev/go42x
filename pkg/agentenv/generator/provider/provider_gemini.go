package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const Gemini = "gemini"

const (
	geminiSettingsDir      = ".gemini"
	geminiSettingsFile     = "settings.json"
	mcpDefaultTimeout      = 30000 // in milliseconds
	mcpDefaultTrust        = true
	maxSessionsTurns       = 10
	maxSessionDuration     = 600 // in seconds
	checkpointingEnabled   = true
	autoAcceptEnabled      = true
	usageStatisticsEnabled = false
)

// GeminiSettings represents .gemini/settings.json structure
type GeminiSettings struct {
	CoreTools              []string                         `json:"coreTools"`
	ExcludeTools           []string                         `json:"excludeTools"`
	MaxSessionTurns        int                              `json:"maxSessionTurns"`
	MaxSessionDuration     int                              `json:"maxSessionDuration"`
	Checkpointing          GeminiCheckpointing              `json:"checkpointing"`
	AutoAccept             bool                             `json:"autoAccept"`
	MCPServers             map[string]GeminiMCPServerConfig `json:"mcpServers"`
	AllowMCPServers        []string                         `json:"allowMCPServers"`
	UsageStatisticsEnabled bool                             `json:"usageStatisticsEnabled"`
}

type GeminiCheckpointing struct {
	Enabled bool `json:"enabled"`
}

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
	allTools := p.collectAllTools(providerConfig)
	enabledServers, mcpServers := p.extractMCPServers(&allTools)

	// Generate .gemini/settings.json
	geminiSettings := GeminiSettings{
		CoreTools:              allTools,
		ExcludeTools:           []string{},
		MaxSessionTurns:        maxSessionsTurns,
		MaxSessionDuration:     maxSessionDuration,
		Checkpointing:          GeminiCheckpointing{Enabled: checkpointingEnabled},
		AutoAccept:             autoAcceptEnabled,
		MCPServers:             mcpServers,
		AllowMCPServers:        enabledServers,
		UsageStatisticsEnabled: usageStatisticsEnabled,
	}

	geminiDir := filepath.Join(p.outputDir, geminiSettingsDir)
	settingsPath := filepath.Join(geminiDir, geminiSettingsFile)
	if err := p.writeJSONFile(settingsPath, geminiSettings); err != nil {
		return fmt.Errorf("failed to write %s: %w", settingsPath, err)
	}

	p.logger.Info("Generated output", "file", settingsPath)

	return nil
}

func (p *GeminiProvider) collectAllTools(providerConfig config.Provider) []string {
	allTools := make([]string, 0, len(providerConfig.Tools))
	allTools = append(allTools, providerConfig.Tools...)
	return allTools
}

func (p *GeminiProvider) extractMCPServers(allTools *[]string) ([]string, map[string]GeminiMCPServerConfig) {
	enabledServers := []string{}
	mcpServers := make(map[string]GeminiMCPServerConfig)

	for name, server := range p.config.MCP {
		if server.Enabled {
			enabledServers = append(enabledServers, name)
			*allTools = append(*allTools, server.Tools...)
			mcpServers[name] = GeminiMCPServerConfig{
				Command: server.Command,
				Args:    server.Args,
				Env:     server.Env,
				Timeout: mcpDefaultTimeout,
				Trust:   mcpDefaultTrust,
				URL:     server.URL,
				Headers: server.Headers,
				CWD:     server.CWD,
			}
		}
	}

	return enabledServers, mcpServers
}

func (p *GeminiProvider) writeJSONFile(path string, data interface{}) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return p.writeOutput(path, string(content))
}
