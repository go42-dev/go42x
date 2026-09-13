package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/version"
)

func NewVersionCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Version information",
		Long:  `Version information`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVersionCommand(f)
		},
	}
	return cmd
}

func runVersionCommand(f *cmdutil.Factory) error {
	_, err := fmt.Fprintf(
		f.Output(),
		"Version: %s\nGo:      %s\nOS/Arch: %s/%s\n",
		version.GetVersion(),
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
	return err
}
