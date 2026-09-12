package agentenv

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator"
)

const (
	agentEnvDir = ".go42x"
	configFile  = "go42x.yaml"
)

type Service struct {
	logger   *slog.Logger
	settings *Settings
}

func NewAgentEnvService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}

	svc := &Service{
		settings: settings,
	}

	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}

	return svc, nil
}

// Init initializes the agentenv environment
func (s *Service) Init(_ context.Context) error {
	s.logger.Info("Initializing agentenv")

	targetDir := filepath.Join(s.settings.OutputPath, agentEnvDir)
	if _, err := os.Stat(filepath.Join(targetDir, configFile)); err == nil {
		s.logger.Info("Configuration already exists")
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check configuration: %w", err)
	}

	s.logger.Info("Creating default configuration")

	if err := extractTemplate(targetDir); err != nil {
		return fmt.Errorf("failed to extract template: %w", err)
	}

	if err := updateGitIgnore(s.settings.OutputPath); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}

	s.logger.Info("agentenv initialized successfully")

	return nil
}

// Generate generates the agent environment configuration
func (s *Service) Generate(ctx context.Context) error {
	absolutePath, err := filepath.Abs(s.settings.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	s.logger.Info("Generating agentenv", "dir", absolutePath)

	cfgPath := filepath.Join(s.settings.OutputPath, agentEnvDir, configFile)
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	templateDir := filepath.Join(s.settings.OutputPath, agentEnvDir)
	if s.settings.GenerateClean {
		s.logger.Info("Cleaning generated instructions", "dir", s.settings.OutputPath)
		for _, provider := range cfg.Providers {
			outputPath := filepath.Join(s.settings.OutputPath, provider.Output)
			if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove generated file %s: %w", outputPath, err)
			}
		}
	}

	gen := generator.NewGenerator(
		s.logger.With("component", "generator"),
		cfg, templateDir, s.settings.OutputPath,
	)

	if err := gen.Generate(ctx); err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	s.logger.Info("Generation completed")

	return nil
}

const (
	gitignoreFile   = ".gitignore"
	gitignoreMarker = "# agentenv"
)

var ignoreFiles = []string{
	".go42x/kwb/",
	".claude/",
	".mcp.json",
	"CLAUDE.md",
	".gemini/",
	"GEMINI.md",
	".crush/",
	".crush.json",
	"CRUSH.md",
	".github/copilot-instructions.md",
	".github/.copilot.mcp.json",
}

func updateGitIgnore(outputPath string) error {
	gitignorePath := filepath.Join(outputPath, gitignoreFile)

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat .gitignore: %w", err)
	}

	if stat.Size() > 0 {
		// Check if the marker already exists anywhere in the file
		content, err := os.ReadFile(gitignorePath)
		if err != nil {
			return fmt.Errorf("failed to read .gitignore: %w", err)
		}
		if bytes.Contains(content, []byte(gitignoreMarker)) {
			// Marker already exists, no need to add again
			return nil
		}
	}

	if _, err := f.WriteString("\n" + gitignoreMarker + "\n"); err != nil {
		return fmt.Errorf("failed to write to .gitignore: %w", err)
	}

	for _, file := range ignoreFiles {
		if _, err := f.WriteString(file + "\n"); err != nil {
			return fmt.Errorf("failed to write to .gitignore: %w", err)
		}
	}

	return nil
}
