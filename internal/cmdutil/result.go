package cmdutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// ExitError separates process status from Cobra's error-display policy.
type ExitError struct {
	Code     int
	Err      error
	Reported bool
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

func UsageError(err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: 2, Err: err}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	return 1
}

func JSON(output io.Writer, value any) error {
	return json.NewEncoder(output).Encode(value)
}

// ConfigureCommands classifies argument/flag errors and suppresses duplicate
// output for errors already reported by a runner. Call after adding all commands.
func ConfigureCommands(cmd *cobra.Command) {
	if cmd.Args != nil {
		args := cmd.Args
		cmd.Args = func(c *cobra.Command, values []string) error { return UsageError(args(c, values)) }
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return UsageError(err) })
	if cmd.RunE != nil {
		run, silenceErrors := cmd.RunE, cmd.SilenceErrors
		cmd.RunE = func(c *cobra.Command, args []string) error {
			c.SilenceErrors = silenceErrors
			err := run(c, args)
			var exit *ExitError
			if errors.As(err, &exit) && exit.Reported {
				c.SilenceErrors = true
			}
			return err
		}
	}
	for _, child := range cmd.Commands() {
		ConfigureCommands(child)
	}
}

func FailedChecks() error {
	return &ExitError{Code: 1, Err: fmt.Errorf("doctor found failed checks"), Reported: true}
}
