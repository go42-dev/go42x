package vendoring

import (
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/vendoring"
)

const (
	flagDryRun   = "dry-run"
	flagForce    = "force"
	flagJSON     = "json"
	flagLock     = "lock"
	flagManifest = "manifest"
	flagRoot     = "root"
)

func NewVendoringCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := vendoring.NewSettings()

	cmd := &cobra.Command{
		Use:   "vendor",
		Short: "Manage selected upstream files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().String(
		flagRoot, defaults.RootPath,
		"project root")
	cmd.PersistentFlags().String(
		flagManifest, defaults.ManifestPath,
		"manifest path relative to project root")
	cmd.PersistentFlags().String(
		flagLock, "",
		"lock path relative to project root; defaults beside the manifest")
	cmd.PersistentFlags().Bool(
		flagJSON, false,
		"output structured results")

	cmd.AddCommand(
		newCheckCommand(f),
		newSyncCommand(f),
		newUpdateCommand(f),
		newTouchCommand(f),
	)

	return cmd
}
