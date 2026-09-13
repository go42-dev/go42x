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

// Init initializes the agentenv environment in the current directory.
func (s *Service) Init(_ context.Context) error {
	s.logger.Info("Initializing agentenv")

	targetDir := agentEnvDir
	if _, err := os.Stat(filepath.Join(targetDir, configFile)); err == nil {
		s.logger.Info("Configuration already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check configuration: %w", err)
	}

	s.logger.Info("Installing missing configuration templates")

	if err := extractTemplate(targetDir); err != nil {
		return fmt.Errorf("failed to extract template: %w", err)
	}

	if err := updateGitIgnore("."); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}

	s.logger.Info("agentenv initialized successfully")

	return nil
}

// Generate generates the agent environment configuration in the current directory.
func (s *Service) Generate(ctx context.Context) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	s.logger.Info("Generating agentenv", "dir", workingDir)

	templateDir := filepath.Join(workingDir, agentEnvDir)
	cfgPath := filepath.Join(templateDir, configFile)
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	gen := generator.NewGenerator(
		s.logger.With("component", "generator"),
		cfg, templateDir, workingDir,
	)

	if err := gen.Generate(ctx, s.settings.Clean); err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	s.logger.Info("Generation completed")

	return nil
}

const (
	gitignoreFile   = ".gitignore"
	gitignoreMarker = "# go42x generated files"
)

var ignoreFiles = []string{
	".go42x/kwb/",
	".go42x/backups/",
	".mcp.json",
	".claude/",
	"CLAUDE.md",
	".codex/",
	"AGENTS.md",
	".gemini/",
	"GEMINI.md",
	".crush/",
	".crush.json",
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

	var content []byte
	if stat.Size() > 0 {
		content, err = os.ReadFile(gitignorePath)
		if err != nil {
			return fmt.Errorf("failed to read .gitignore: %w", err)
		}
	}

	existing := make(map[string]bool)
	for line := range bytes.SplitSeq(content, []byte("\n")) {
		existing[string(bytes.TrimSpace(line))] = true
	}

	var additions bytes.Buffer
	if !existing[gitignoreMarker] {
		additions.WriteString(gitignoreMarker + "\n")
	}

	for _, file := range ignoreFiles {
		if !existing[file] {
			additions.WriteString(file + "\n")
		}
	}
	if additions.Len() == 0 {
		return nil
	}

	if _, err := f.WriteString("\n" + additions.String()); err != nil {
		return fmt.Errorf("failed to write to .gitignore: %w", err)
	}

	return nil
}
