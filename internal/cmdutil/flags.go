package cmdutil

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ExplicitFlag reports whether a flag or its GO42X environment variable was set.
func ExplicitFlag(cmd *cobra.Command, name string) bool {
	_, env := os.LookupEnv("GO42X_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")))
	return cmd.Flags().Changed(name) || env
}
