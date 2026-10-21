package cli

import (
	"fmt"
	"io"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
)

func ReportBuild(output io.Writer, settings *kwb.Settings, result *kwb.BuildResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if result != nil && len(result.BackupDir) > 0 {
				if _, err := fmt.Fprintf(w, "Backup: %s\n", result.BackupDir); err != nil {
					return err
				}
			}
			if runErr != nil {
				return nil
			}

			_, err := fmt.Fprintf(w,
				"Knowledge base updated: %d indexed, %d unchanged, %d deleted; %d files, %d chunks.\n",
				result.Indexed, result.Unchanged, result.Deleted, result.Files, result.Chunks)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportSearch(output io.Writer, settings *kwb.SearchSettings, result *kwb.SearchResponse, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}

			for _, hit := range result.Results {
				if _, err := fmt.Fprintf(w, "%s:%d-%d %s\n%s\n\n",
					hit.Path, hit.StartLine, hit.EndLine, hit.Title, hit.Snippet); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, "%d matches\n", result.Total); err != nil {
				return err
			}
			if result.NextOffset != nil {
				_, err := fmt.Fprintf(w, "Next offset: %d\n", *result.NextOffset)
				return err
			}

			return nil
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportRead(
	output, errorOutput io.Writer,
	settings *kwb.ReadSettings,
	result *kwb.FileContent,
	runErr error,
) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}

			if _, err := fmt.Fprint(w, result.Content); err != nil {
				return err
			}
			if result.NextStartLine != nil {
				_, err := fmt.Fprintf(errorOutput, "Continue with --start-line=%d\n", *result.NextStartLine)
				return err
			}

			return nil
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportStats(output io.Writer, settings *kwb.StatsSettings, result *kwb.Stats, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}

			_, err := fmt.Fprintf(w, "Root: %s\nIndex: %s\nGeneration: %s\nFiles: %d\nChunks: %d\n",
				result.RootPath, result.IndexPath, result.Generation, result.DocumentCount, result.ChunkCount)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportCheck(output io.Writer, settings *kwb.CheckSettings, result *kwb.FreshnessResult, runErr error) error {
	if result != nil && (runErr != nil || !result.Complete || result.Status != kwb.FreshnessFresh) {
		runErr = cmdutil.ReportedError(fmt.Errorf("index freshness: %s", result.Status))
	}
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if result == nil {
				return nil
			}

			if _, err := fmt.Fprintf(w,
				"%s: %d checked; %d new, %d changed, %d deleted, %d excluded\nGeneration: %s\n",
				result.Status, result.CheckedFiles, result.New.Count, result.Changed.Count,
				result.Deleted.Count, result.Excluded.Count, result.Generation); err != nil {
				return err
			}

			for _, diagnostic := range result.Diagnostics {
				if _, err := fmt.Fprintln(w, diagnostic.Message); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}
