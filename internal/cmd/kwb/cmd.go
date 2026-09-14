package kwb

import (
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func NewKnowledgeBaseCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := kwb.NewSettings()
	cmd := &cobra.Command{
		Use:   "kwb",
		Short: "Manage the knowledge base",
		Long:  "Build, search, and read the knowledge base index.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().
		String("index", defaults.IndexPath, "path to the index")
	cmd.PersistentFlags().
		String("root", defaults.RootPath, "root directory to index")
	cmd.PersistentFlags().
		Duration("search-timeout", defaults.SearchTimeout, "maximum duration of a knowledge-base read")

	cmd.AddCommand(newBuildCommand(f), newSearchCommand(f), newReadCommand(f), newStatsCommand(f), newCheckCommand(f))

	return cmd
}
