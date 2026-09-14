package cmdutil

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

// ProviderSelection gives the explicit flag precedence over the environment,
// preserving the distinction between an absent value and an empty selection.
func ProviderSelection(cmd *cobra.Command) ([]string, error) {
	value, set := os.LookupEnv("GO42X_PROVIDERS")
	if cmd.Flags().Changed("providers") {
		var err error
		value, err = cmd.Flags().GetString("providers")
		if err != nil {
			return nil, err
		}
		set = true
	}
	if !set {
		return nil, nil
	}
	return config.ParseProviders(value)
}
