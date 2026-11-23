package journal

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

const (
	flagApply    = "apply"
	flagIssue    = "issue"
	flagJSON     = "json"
	flagKeep     = "keep"
	flagKept     = "kept"
	flagLimit    = "limit"
	flagName     = "name"
	flagPR       = "pr"
	flagRuntime  = "runtime"
	flagSections = "sections"
	flagSession  = "session"
	flagSince    = "since"
	flagStray    = "stray"
	flagSummary  = "summary"
	flagTag      = "tag"
	flagTitle    = "title"
)

// NewJournalCommand returns the journal command group, registered below agentenv.
func NewJournalCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Record, continue, and clean up agent task entries",
		Long: `Keep the material of each task in a journal entry in the journal directory, .go42x/journal
unless journal.path in .go42x/go42x.yaml sets another: an entry.json record, notes.md handoff
notes, and the task's files. Any session or runtime can list entries and continue them. gc
deletes entries that go without an update for journal.retention-days, 30 by default, unless
they are kept. The journal is ignored by Git; in CI, the generated 901-aw-journal.md workflow
import can restore and save it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newNewCommand(f))
	cmd.AddCommand(newListCommand(f))
	cmd.AddCommand(newShowCommand(f))
	cmd.AddCommand(newNoteCommand(f))
	cmd.AddCommand(newAttachCommand(f))
	cmd.AddCommand(newSearchCommand(f))
	cmd.AddCommand(newIndexCommand(f))
	cmd.AddCommand(newGCCommand(f))

	return cmd
}

// addActorFlags registers the flags of commands that change entries.
func addActorFlags(cmd *cobra.Command) {
	cmd.Flags().String(
		flagRuntime, "",
		"runtime making the change, such as claude, codex, or human; detected when empty")
	cmd.Flags().String(
		flagSession, "",
		"session identifier of the runtime; detected when empty")
}

// newSettings builds the settings shared by journal commands. Commands that change
// entries also record the detected runtime and session.
func newSettings(f *cmdutil.Factory, actor bool) *journal.Settings {
	settings := journal.NewSettings()
	settings.JSON = viper.GetBool(flagJSON)
	if actor {
		settings.Actor = journal.DetectActor(
			viper.GetString(flagRuntime),
			viper.GetString(flagSession),
			os.LookupEnv,
			isTerminal(f.Input()),
		)
	}
	return settings
}

// isTerminal reports whether input is an interactive terminal, which indicates a person.
func isTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
