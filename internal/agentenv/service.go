package agentenv

import (
	"context"
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

// InitResult describes an initialization. A forced reinstall also reports its backup
// and replacements.
type InitResult struct {
	*fspatch.UpdateResult
	Directory string `json:"directory"`
}

type GenerateResult struct {
	Changes []fspatch.Change `json:"changes"`
}

// UpdateResult describes an update. Overwritten lists project-relative paths of
// release-owned sources whose local changes a merge update replaced; the backup keeps
// the originals.
type UpdateResult struct {
	fspatch.UpdateResult
	Overwritten []string `json:"overwritten"`
}

type Service struct {
	logger   *slog.Logger
	settings *Settings
	config   *config.Config
	// source identifies this binary's bundle; it is nil for builds without a published revision.
	source *buildinfo.Source
}

func NewAgentEnvService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}

	svc := &Service{
		settings: settings,
	}
	if source, ok := buildinfo.CurrentSource(); ok {
		svc.source = &source
	}

	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}
	svc.logger = svc.logger.With(slog.String("component", "agentenv"))

	return svc, nil
}

// Init installs missing bundled sources in the current directory and keeps existing
// files. A complete installation records this binary's bundle for later updates. With
// Force, Init reinstalls every bundled source instead.
func (s *Service) Init(ctx context.Context) (*InitResult, error) {
	if s.settings.Force {
		return s.reinstall(ctx)
	}

	s.logger.InfoContext(ctx, "Initializing agentenv")

	if _, err := os.Stat(filepath.Join(config.Directory, config.FileProject)); err == nil {
		s.logger.InfoContext(ctx, "Configuration already exists")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to check configuration: %w", err)
	}

	s.logger.InfoContext(ctx, "Installing missing configuration templates")

	complete, err := extractBundle(ctx, config.Directory)
	if err != nil {
		return nil, fmt.Errorf("failed to extract template: %w", err)
	}
	// Kept files may come from another release, so only a complete installation is recorded.
	if complete {
		if err := writeBundleSource(config.Directory, s.source); err != nil {
			return nil, fmt.Errorf("record bundle: %w", err)
		}
	}

	if err := updateGitIgnore("."); err != nil {
		return nil, fmt.Errorf("failed to update .gitignore: %w", err)
	}

	return &InitResult{Directory: config.Directory}, nil
}

// reinstall replaces every bundled source with this binary's version, including unchanged
// files, and records its bundle. Originals are backed up before application. Local
// configuration and additional sources are kept.
func (s *Service) reinstall(ctx context.Context) (*InitResult, error) {
	s.logger.InfoContext(ctx, "Reinstalling bundled agentenv sources")

	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(root, config.Directory)
	plan := fspatch.NewPlan(s.logger, root, filepath.Join(dir, "backups"))
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		return nil, err
	}

	updatedConfig, err := fs.ReadFile(bundle, config.FileProject)
	if err != nil {
		return nil, fmt.Errorf("read bundled configuration: %w", err)
	}
	if err := validateUpdatedConfig(plan, root, pinBundledSchemaURL(config.FileProject, updatedConfig)); err != nil {
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

		data = pinBundledSchemaURL(path, data)
		return plan.Write(filepath.Join(dir, filepath.FromSlash(path)), data, fspatch.KindSources, true)
	})
	if err != nil {
		return nil, fmt.Errorf("prepare bundled sources: %w", err)
	}

	if err := prepareBundleSource(plan, dir, s.source); err != nil {
		return nil, fmt.Errorf("prepare bundle record: %w", err)
	}
	if err := prepareGitIgnore(plan, root); err != nil {
		return nil, fmt.Errorf("prepare .gitignore: %w", err)
	}

	applied, err := plan.ApplyUpdate(ctx, buildinfo.GetVersion())
	if err != nil {
		if applied != nil {
			return &InitResult{
				Directory:    config.Directory,
				UpdateResult: applied,
			}, fmt.Errorf("reinstall stopped after %d replacements; recovery backup: %s: %w",
				applied.Applied, applied.BackupDir, err)
		}

		return nil, fmt.Errorf("reinstall failed before application: %w", err)
	}

	return &InitResult{
		Directory:    config.Directory,
		UpdateResult: applied,
	}, nil
}

// validateUpdatedConfig checks the configuration that an update or reinstall installs,
// together with the project's local overrides.
func validateUpdatedConfig(plan *fspatch.Plan, root string, updatedConfig []byte) error {
	local, _, err := plan.Read(filepath.Join(root, config.Directory, config.FileLocal))
	if err != nil {
		return fmt.Errorf("read local configuration: %w", err)
	}

	variables, err := config.VariablesForProject(root)
	if err != nil {
		return err
	}

	if _, err := config.ParseProjectConfig(updatedConfig, local, nil, variables); err != nil {
		return fmt.Errorf("validate updated configuration: %w", err)
	}

	return nil
}

// Update merges bundled sources and refreshes the managed .gitignore block without
// generating outputs. It fetches the previous bundle that the project's bundle record
// names and merges project changes with this binary's bundle in three ways. Bundled
// resources, skills, and upstream files are replaced, the vendoring lock is rebuilt from
// the merged manifest, and the record names this binary's bundle afterward. Originals
// are backed up before application. Local configuration and additional sources are kept.
func (s *Service) Update(ctx context.Context) (*UpdateResult, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	templateDir := filepath.Join(root, config.Directory)

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

	plan := fspatch.NewPlan(s.logger, root, filepath.Join(root, config.Directory, "backups"))
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		return nil, err
	}

	updatedConfig, overwritten, err := prepareMergedBundle(ctx, root, plan, bundle, s.source)
	if err != nil {
		return &UpdateResult{Overwritten: []string{}}, fmt.Errorf("prepare merged sources: %w", err)
	}
	if err := validateUpdatedConfig(plan, root, updatedConfig); err != nil {
		return nil, err
	}

	if err := prepareGitIgnore(plan, root); err != nil {
		return nil, fmt.Errorf("prepare .gitignore: %w", err)
	}

	applied, err := plan.ApplyUpdate(ctx, buildinfo.GetVersion())
	if err != nil {
		if applied != nil {
			result := &UpdateResult{
				UpdateResult: *applied,
				Overwritten:  overwritten,
			}
			return result, fmt.Errorf("update stopped after %d replacements; recovery backup: %s: %w",
				result.Applied, result.BackupDir, err)
		}

		return nil, fmt.Errorf("update failed before application: %w", err)
	}

	return &UpdateResult{
		UpdateResult: *applied,
		Overwritten:  overwritten,
	}, nil
}

// Generate generates the agent environment configuration in the current directory.
func (s *Service) Generate(ctx context.Context) (*GenerateResult, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	s.logger.InfoContext(ctx, "Generating agentenv", slog.String("dir", workingDir))

	templateDir := filepath.Join(workingDir, config.Directory)
	cfgPath := filepath.Join(templateDir, config.FileProject)
	cfg, err := config.LoadProjectConfig(
		cfgPath,
		s.settings.Providers,
		config.WithLocalOverrides(!s.settings.NoLocal),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	gen := generator.NewGenerator(
		s.logger.With(slog.String("subsystem", "generator")),
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
