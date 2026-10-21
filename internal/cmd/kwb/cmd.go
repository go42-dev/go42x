package kwb

import (
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
)

const (
	flagBatchSize     = "batch-size"
	flagEndLine       = "end-line"
	flagExcludeDir    = "exclude-dir"
	flagExcludeFile   = "exclude-file"
	flagIncludeExt    = "include-ext"
	flagIndex         = "index"
	flagIndexType     = "index-type"
	flagJSON          = "json"
	flagKind          = "kind"
	flagLanguage      = "language"
	flagLimit         = "limit"
	flagMaxFileSize   = "max-file-size"
	flagOffset        = "offset"
	flagPathPrefix    = "path-prefix"
	flagRebuild       = "rebuild"
	flagRoot          = "root"
	flagSearchTimeout = "search-timeout"
	flagStartLine     = "start-line"
)

func NewKnowledgeBaseCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := kwb.NewSettings()

	cmd := &cobra.Command{
		Use:   "kwb",
		Short: "Manage the project knowledge base",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().String(
		flagIndex, defaults.IndexPath,
		"path to the index")
	cmd.PersistentFlags().String(
		flagRoot, defaults.RootPath,
		"project root")
	cmd.PersistentFlags().Duration(
		flagSearchTimeout, defaults.SearchTimeout,
		"maximum duration of knowledge-base reads and freshness checks")

	cmd.AddCommand(
		newBuildCommand(f),
		newSearchCommand(f),
		newReadCommand(f),
		newStatsCommand(f),
		newCheckCommand(f),
	)

	return cmd
}

// queryKnowledgeBaseSettings assembles common read options at the CLI boundary.
func queryKnowledgeBaseSettings(cmd *cobra.Command) *kwb.Settings {
	settings := kwb.NewSettings()
	defaultIndexPath := settings.IndexPath
	settings.RootPath = viper.GetString(flagRoot)
	settings.IndexPath = viper.GetString(flagIndex)
	settings.RequireRootMatch = cmdutil.ExplicitFlag(cmd, flagRoot)
	settings.SearchTimeout = viper.GetDuration(flagSearchTimeout)

	if settings.RequireRootMatch && !cmdutil.ExplicitFlag(cmd, flagIndex) {
		settings.IndexPath = filepath.Join(settings.RootPath, defaultIndexPath)
	}

	return settings
}
