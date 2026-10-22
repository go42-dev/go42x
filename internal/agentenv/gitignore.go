package agentenv

import (
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

func updateGitIgnore(root string) error {
	plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), root, filepath.Join(root, config.Directory, "backups"))
	if err := prepareGitIgnore(plan, root); err != nil {
		return err
	}

	return plan.Apply()
}

func prepareGitIgnore(plan *fspatch.Plan, root string) error {
	return plan.WriteManagedBlock(filepath.Join(root, ".gitignore"), assets.AgentEnvGitIgnore(), fspatch.KindSources)
}
