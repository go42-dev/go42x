package provider

import (
	"log/slog"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

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
