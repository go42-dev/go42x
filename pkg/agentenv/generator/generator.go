package generator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/collector"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/provider"
)

type Generator struct {
	logger      *slog.Logger
	config      *config.Config
	providers   map[string]providerAccessor
	templateDir string
	outputDir   string
}

func NewGenerator(logger *slog.Logger, cfg *config.Config, templateDir, outputDir string) *Generator {
	g := &Generator{
		logger:      logger,
		config:      cfg,
		providers:   make(map[string]providerAccessor),
		templateDir: templateDir,
		outputDir:   outputDir,
	}

	g.registerProviders()

	return g
}

func (g *Generator) registerProviders() {
	templateEngine := newTemplateEngine(g.templateDir)

	g.providers[provider.Claude] = provider.NewClaudeProvider(
		g.logger.With("provider", provider.Claude),
		g.config, templateEngine, g.templateDir, g.outputDir)

	g.providers[provider.Gemini] = provider.NewGeminiProvider(
		g.logger.With("provider", provider.Gemini),
		g.config, templateEngine, g.templateDir, g.outputDir)

	g.providers[provider.Crush] = provider.NewCrushProvider(
		g.logger.With("provider", provider.Crush),
		g.config, templateEngine, g.templateDir, g.outputDir)

	g.providers[provider.Copilot] = provider.NewCopilotProvider(
		g.logger.With("provider", provider.Copilot),
		g.config, templateEngine, g.templateDir, g.outputDir)
}

func (g *Generator) Generate(ctx context.Context) error {
	g.logger.Info("Starting generation")

	tplCtx, err := g.buildTemplateContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to build context: %w", err)
	}

	for name, providerConfig := range g.config.Providers {
		p, exists := g.providers[name]
		if !exists {
			g.logger.Warn("Unknown provider", "provider", name)
			continue
		}
		if err := p.Generate(tplCtx.ToMap(), providerConfig); err != nil {
			g.logger.Error("Provider generation failed", "provider", name, "error", err)
			continue
		}
	}

	return nil
}

func (g *Generator) buildTemplateContext(ctx context.Context) (*Context, error) {
	var (
		tplCtx = newContext(ctx)
	)

	collectors := []collectorAccessor{
		collector.NewGitCollector(),
		collector.NewProjectCollector(g.config),
		collector.NewEnvironmentCollector(g.config.EnvVars),
		collector.NewGitHubActionsCollector(),
		collector.NewAnalysisCollector(g.templateDir),
		collector.NewConventionsCollector(g.outputDir),
	}

	sort.Slice(collectors, func(i, j int) bool {
		return collectors[i].Priority() < collectors[j].Priority()
	})

	collected := make(map[string]interface{})

	for _, c := range collectors {
		data, err := c.Collect(ctx)
		if err != nil {
			g.logger.Error("Collector failed", "collector", c.Name(), "error", err)
			continue
		}
		if len(data) > 0 {
			collected[c.Name()] = data
		}
	}

	for k, v := range collected {
		tplCtx.Set(k, v)
	}

	return tplCtx, nil
}
