package provider

import (
	"log/slog"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

const AgentsFile = "AGENTS.md"

type BaseProvider struct {
	config         *config.Config
	logger         *slog.Logger
	templateDir    string
	outputDir      string
	templateEngine TemplateEngineAccessor
}

func NewBaseProvider(
	logger *slog.Logger,
	cfg *config.Config,
	templateEngine TemplateEngineAccessor,
	templateDir, outputDir string,
) *BaseProvider {
	return &BaseProvider{
		config:         cfg,
		logger:         logger,
		templateDir:    templateDir,
		outputDir:      outputDir,
		templateEngine: templateEngine,
	}
}

// InstructionsFileName defaults to the shared instructions document.
func (p *BaseProvider) InstructionsFileName() string {
	return AgentsFile
}
