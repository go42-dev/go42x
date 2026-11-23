package agentenv

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/agentenv/generator"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

// gitIgnoreBlocks pairs each managed .gitignore, relative to the project root, with the
// template of its block.
var gitIgnoreBlocks = []struct{ path, template string }{
	{".gitignore", assets.ProjectGitIgnore},
	{config.Directory + "/.gitignore", assets.DirGitIgnore},
}

// updateGitIgnore refreshes the managed .gitignore blocks, rendered with the template
// context data.
func updateGitIgnore(root string, data map[string]any) error {
	plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), root, filepath.Join(root, config.Directory, "backups"))
	if err := prepareGitIgnore(plan, root, data); err != nil {
		return err
	}

	return plan.Apply()
}

// prepareGitIgnore refreshes the managed blocks of the project .gitignore, which ignores
// generated agent files, and of .go42x/.gitignore, which ignores the state directories
// inside .go42x. Their templates render with the template context data.
func prepareGitIgnore(plan *fspatch.Plan, root string, data map[string]any) error {
	for _, block := range gitIgnoreBlocks {
		content, err := assets.Render(block.template, data)
		if err != nil {
			return err
		}
		path := filepath.Join(root, filepath.FromSlash(block.path))
		if err := plan.WriteManagedBlock(path, content, fspatch.KindSources); err != nil {
			return err
		}
	}

	return nil
}

// templateContext returns the template context of cfg for the project at root, without
// logging collector failures; generation reports those.
func templateContext(ctx context.Context, cfg *config.Config, root string) (map[string]any, error) {
	return generator.NewGenerator(
		slog.New(slog.DiscardHandler),
		cfg,
		filepath.Join(root, config.Directory),
		root,
	).TemplateContext(ctx)
}
