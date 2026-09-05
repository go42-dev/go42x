package provider

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

func (p *BaseProvider) loadTemplate(path string) (string, error) {
	fullPath := filepath.Join(p.templateDir, path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read template %s: %w", path, err)
	}
	return string(data), nil
}

func (p *BaseProvider) loadTemplates(paths []string) ([]string, error) {
	var contents []string
	for _, path := range paths {
		text, err := p.loadTemplate(path)
		if err != nil {
			return nil, err
		}
		contents = append(contents, text)
	}
	return contents, nil
}

func (p *BaseProvider) writeOutput(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}
	return nil
}

func (p *BaseProvider) mergeStrings(items []string) string {
	var result strings.Builder
	for i, item := range items {
		if i > 0 {
			result.WriteString("\n\n")
		}
		result.WriteString(strings.TrimSpace(item))
	}
	return result.String()
}
