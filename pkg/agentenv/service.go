package agentenv

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/version"
	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/provider"
)

const (
	agentEnvDir     = ".go42x"
	configFile      = "go42x.yaml"
	schemaFile      = "go42x.schema.json"
	gitignoreFile   = ".gitignore"
	gitignoreMarker = "# go42x generated files"
)

type Service struct {
	logger   *slog.Logger
	settings *Settings
	config   *config.Config
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
// Existing configuration and templates are preserved; the schema is refreshed.
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

	s.logger.Info("Updating configuration schema")
	// #nosec G306 -- The embedded JSON schema is public project metadata, so 0644 is intentional.
	if err := os.WriteFile(filepath.Join(targetDir, schemaFile), []byte(config.Schema()), 0644); err != nil {
		return fmt.Errorf("failed to update configuration schema: %w", err)
	}

	if err := updateGitIgnore("."); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}

	s.logger.Info("agentenv initialized successfully")

	return nil
}

// Update replaces bundled sources and regenerates enabled-provider outputs.
// Every existing destination is backed up before any replacement, even if its
// contents are identical. Local configuration and additional sources are kept.
func (s *Service) Update(ctx context.Context) (*output.UpdateResult, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	templateDir := filepath.Join(root, agentEnvDir)
	info, err := os.Lstat(templateDir)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("agentenv is not initialized; run go42x agentenv init")
	}
	if err != nil {
		return nil, fmt.Errorf("read agentenv directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("agentenv source must be a directory: %s", templateDir)
	}
	stage, cleanup, err := newUpdateStage(root)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	plan := output.NewPlan(s.logger, root)
	if err := copyUpdateSources(ctx, plan, templateDir, stage); err != nil {
		return nil, fmt.Errorf("stage project sources: %w", err)
	}
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(bundle, path)
		if err != nil {
			return err
		}
		return stageUpdateSource(plan, templateDir, stage, filepath.FromSlash(path), data)
	})
	if err != nil {
		return nil, fmt.Errorf("prepare bundled sources: %w", err)
	}
	if err := stageUpdateSource(plan, templateDir, stage, schemaFile, []byte(config.Schema())); err != nil {
		return nil, fmt.Errorf("prepare configuration schema: %w", err)
	}
	cfg, err := config.LoadProjectConfig(filepath.Join(stage, configFile), s.settings.Providers)
	if err != nil {
		return nil, fmt.Errorf("validate updated configuration: %w", err)
	}
	if err := rebaseUpdateTemplates(cfg, templateDir, stage); err != nil {
		return nil, err
	}
	gen := generator.NewGenerator(s.logger.With("component", "generator"), cfg, stage, root)
	generated, err := gen.Prepare(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("prepare updated outputs: %w", err)
	}
	if err := plan.Merge(generated); err != nil {
		return nil, err
	}
	result, err := plan.ApplyUpdate(ctx, version.GetVersion())
	if err != nil {
		if result != nil {
			return result, fmt.Errorf("update stopped after %d replacements; recovery backup: %s: %w",
				result.Applied, result.BackupDir, err)
		}
		return nil, fmt.Errorf("update failed before application: %w", err)
	}
	return result, nil
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
	cfg, err := config.LoadProjectConfig(cfgPath, s.settings.Providers)
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

func newUpdateStage(root string) (string, func(), error) {
	buildDir := filepath.Join(root, ".build")
	_, err := os.Lstat(buildDir)
	created := os.IsNotExist(err)
	if err != nil && !created {
		return "", nil, err
	}
	if err := os.MkdirAll(buildDir, 0700); err != nil {
		return "", nil, fmt.Errorf("create update staging directory: %w", err)
	}
	stage, err := os.MkdirTemp(buildDir, "agentenv-update-*")
	cleanup := func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
		if created {
			// Remove only an empty directory; other build artifacts are preserved.
			_ = os.Remove(buildDir)
		}
	}
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create update staging directory: %w", err)
	}
	return stage, cleanup, nil
}

func copyUpdateSources(ctx context.Context, plan *output.Plan, source, stage string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "backups" || relative == "kwb" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(stage, relative), 0700)
		}
		data, _, err := plan.Read(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(stage, relative), data, 0600)
	})
}

func stageUpdateSource(plan *output.Plan, source, stage, path string, data []byte) error {
	if err := plan.Write(filepath.Join(source, path), data, output.Sources, true); err != nil {
		return err
	}
	target := filepath.Join(stage, path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0600)
}

// References outside .go42x must still resolve against the real project while
// bundled and additional files inside it are read from the candidate directory.
func rebaseUpdateTemplates(cfg *config.Config, source, stage string) error {
	rebase := func(path string) (string, error) {
		if path == "" {
			return path, nil
		}
		target := filepath.Join(source, path)
		relative, err := filepath.Rel(source, target)
		if err != nil || filepath.IsLocal(relative) {
			return path, err
		}
		return filepath.Rel(stage, target)
	}
	var err error
	if cfg.Context.Template, err = rebase(cfg.Context.Template); err != nil {
		return err
	}
	if cfg.Context.ChunksDir, err = rebase(cfg.Context.ChunksDir); err != nil {
		return err
	}
	for name, provider := range cfg.Providers {
		for i, path := range provider.Agents {
			if provider.Agents[i], err = rebase(path); err != nil {
				return err
			}
		}
		cfg.Providers[name] = provider
	}
	return nil
}

var ignoreFiles = []string{
	".go42x/go42x.local.yaml",
	".go42x/kwb/",
	".go42x/backups/",
	".mcp.json",
	".claude/",
	provider.ClaudeFile,
	".codex/",
	provider.AgentsFile,
	".gemini/",
	provider.GeminiFile,
	".crush/",
	".crush.json",
}

func updateGitIgnore(outputPath string) error {
	gitignorePath := filepath.Join(outputPath, gitignoreFile)

	// #nosec G304 G302 -- This trusted project path holds public ignore rules, which intentionally use 0644.
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
		// #nosec G304 -- Read the same caller-selected .gitignore opened above; generation assumes a trusted project tree.
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
