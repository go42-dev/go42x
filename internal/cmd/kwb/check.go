package kwb

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func newCheckCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "check",
		Short:       "Check index freshness without updating it",
		Long:        "Hash eligible source using recorded build settings and current ignore files.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"result-output": "true"},
		RunE: func(cmd *cobra.Command, _ []string) (retErr error) {
			settings := queryKnowledgeBaseSettings(cmd)
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			service, err := kwb.NewService(settings)
			if err != nil {
				return err
			}
			defer func() { retErr = errors.Join(retErr, service.Close()) }()
			result, checkErr := service.CheckFreshness(f.Context())
			if result == nil {
				return checkErr
			}
			if viper.GetBool("json") {
				err = cmdutil.JSON(f.Output(), result)
			} else {
				_, err = fmt.Fprintf(
					f.Output(),
					"%s: %d checked; %d new, %d changed, %d deleted, %d excluded\nGeneration: %s\n",
					result.Status,
					result.CheckedFiles,
					result.New.Count,
					result.Changed.Count,
					result.Deleted.Count,
					result.Excluded.Count,
					result.Generation,
				)
				for _, diagnostic := range result.Diagnostics {
					if err != nil {
						break
					}
					_, err = fmt.Fprintln(f.Output(), diagnostic.Message)
				}
			}
			if err != nil {
				return err
			}
			if checkErr != nil || !result.Complete || result.Status != "fresh" {
				return &cmdutil.ExitError{
					Code:     1,
					Err:      fmt.Errorf("index freshness: %s", result.Status),
					Reported: true,
				}
			}
			return nil
		},
	}

	cmd.Flags().Bool("json", false, "output structured freshness results")

	return cmd
}
