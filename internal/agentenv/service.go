package agentenv

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/agentenv/generator"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const (
	agentEnvDir = ".go42x"
	configFile  = "go42x.yaml"
)

type InitResult struct {
	Directory string `json:"directory"`
}

// ExportResult identifies the directory containing an exported bundle.
type ExportResult struct {
	Directory string `json:"directory"`
}

type GenerateResult struct {
	Changes []fspatch.Change `json:"changes"`
}

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
// Existing configuration and templates are preserved.
func (s *Service) Init(ctx context.Context) (*InitResult, error) {
	s.logger.Info("Initializing agentenv")

	if _, err := os.Stat(filepath.Join(agentEnvDir, configFile)); err == nil {
		s.logger.Info("Configuration already exists")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to check configuration: %w", err)
	}

	s.logger.Info("Installing missing configuration templates")

	if err := extractBundle(ctx, agentEnvDir); err != nil {
		return nil, fmt.Errorf("failed to extract template: %w", err)
	}

	if err := updateGitIgnore("."); err != nil {
		return nil, fmt.Errorf("failed to update .gitignore: %w", err)
	}

	return &InitResult{Directory: agentEnvDir}, nil
}

// Export writes this binary's bundled sources into a new directory without loading project configuration.
func (s *Service) Export(ctx context.Context, outputDir string) (*ExportResult, error) {
	settings := &ExportSettings{OutputDir: outputDir}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// #nosec G301 -- Bundled sources are public project files.
	if err := os.MkdirAll(filepath.Dir(outputDir), 0755); err != nil {
		return nil, fmt.Errorf("create export parent: %w", err)
	}
	// #nosec G301 -- Require a new directory so exports cannot mix with or replace existing files.
	if err := os.Mkdir(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create export directory: %w", err)
	}

	if err := extractBundle(ctx, outputDir); err != nil {
		return nil, errors.Join(fmt.Errorf("export bundled sources: %w", err), os.RemoveAll(outputDir))
	}

	return &ExportResult{Directory: outputDir}, nil
}

// Update validates and updates bundled sources and the managed .gitignore block without generating outputs.
// BaseDir selects a three-way merge; otherwise bundled files are replaced, including unchanged files.
// Originals are backed up before application. Local configuration and additional sources are kept.
func (s *Service) Update(ctx context.Context) (*fspatch.UpdateResult, error) {
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

	plan := fspatch.NewPlan(s.logger, root, filepath.Join(root, agentEnvDir, "backups"))
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		return nil, err
	}

	var updatedConfig []byte
	if len(s.settings.BaseDir) > 0 {
		updatedConfig, err = s.prepareMergedBundle(ctx, root, plan, bundle)
		if err != nil {
			return &fspatch.UpdateResult{}, fmt.Errorf("prepare merged sources: %w", err)
		}
	} else {
		updatedConfig, err = fs.ReadFile(bundle, configFile)
		if err != nil {
			return nil, fmt.Errorf("read bundled configuration: %w", err)
		}
		updatedConfig = pinBundledSchemaURL(configFile, updatedConfig)
	}
	local, _, err := plan.Read(filepath.Join(templateDir, config.LocalConfigFile))
	if err != nil {
		return nil, fmt.Errorf("read local configuration: %w", err)
	}
	if _, err := config.ParseProjectConfig(updatedConfig, local, nil); err != nil {
		return nil, fmt.Errorf("validate updated configuration: %w", err)
	}

	if len(s.settings.BaseDir) == 0 {
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
			data = pinBundledSchemaURL(path, data)
			return plan.Write(filepath.Join(templateDir, filepath.FromSlash(path)), data, fspatch.Sources, true)
		})
		if err != nil {
			return nil, fmt.Errorf("prepare bundled sources: %w", err)
		}
	}

	if err := prepareGitIgnore(plan, root); err != nil {
		return nil, fmt.Errorf("prepare .gitignore: %w", err)
	}

	result, err := plan.ApplyUpdate(ctx, buildinfo.GetVersion())
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
func (s *Service) Generate(ctx context.Context) (*GenerateResult, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}
	s.logger.Info("Generating agentenv", "dir", workingDir)

	templateDir := filepath.Join(workingDir, agentEnvDir)
	cfgPath := filepath.Join(templateDir, configFile)
	cfg, err := config.LoadProjectConfig(cfgPath, s.settings.Providers)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	gen := generator.NewGenerator(
		s.logger.With("component", "generator"),
		cfg, templateDir, workingDir,
	)

	plan, err := gen.Prepare(ctx, s.settings.Clean)
	if err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}
	if err := plan.Apply(); err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}
	return &GenerateResult{Changes: plan.Changes()}, nil
}
