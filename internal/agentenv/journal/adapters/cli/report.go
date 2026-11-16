package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

// maxShownEvents bounds the log lines that journal show prints as text.
const maxShownEvents = 10

func ReportNew(output io.Writer, settings *journal.Settings, result *journal.EntryResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}
			_, err := fmt.Fprintf(w, "Created journal entry %s in %s; keep handoff notes in %s/notes.md.\n",
				result.Entry.ID, result.Path, result.Path)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportList(
	output io.Writer,
	settings *journal.Settings,
	result *journal.ListResult,
	stray bool,
	runErr error,
) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}

			for _, problem := range result.Invalid {
				if _, err := fmt.Fprintf(w, "Invalid entry %s: %s\n", problem.Path, problem.Error); err != nil {
					return err
				}
			}
			if stray {
				return writeStray(w, result.Stray)
			}
			if len(result.Entries) == 0 {
				_, err := fmt.Fprintln(w, "No journal entries.")
				return err
			}
			for _, entry := range result.Entries {
				if _, err := fmt.Fprintf(w, "%s  %-9s  %s  %s%s\n",
					entry.ID, entry.Status, entry.UpdatedAt.Format(time.DateTime), entry.Title, formatTags(entry.Tags),
				); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func writeStray(w io.Writer, items []journal.StrayItem) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "No content without a journal record.")
		return err
	}

	var size int64
	for _, item := range items {
		size += item.Size
		if _, err := fmt.Fprintf(w, "%s  %s  %s\n",
			item.Path, journal.FormatSize(item.Size), item.ModifiedAt.Format(time.DateOnly),
		); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w,
		"%d items without a journal record (%s); review them and delete what is no longer needed. "+
			"Agents ask the user first.\n",
		len(items), journal.FormatSize(size))
	return err
}

func ReportShow(output io.Writer, settings *journal.Settings, result *journal.ShowResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}

			entry := result.Entry
			expires := "never"
			if entry.ExpiresAt != nil {
				expires = entry.ExpiresAt.Format(time.DateTime)
			}
			if _, err := fmt.Fprintf(
				w,
				"Entry:   %s\nTitle:   %s\nStatus:  %s\nTags:    %s\nCreated: %s\nUpdated: %s by %s\nExpires: %s\nPath:    %s\n",
				entry.ID,
				entry.Title,
				entry.Status,
				strings.Join(entry.Tags, ", "),
				entry.CreatedAt.Format(time.DateTime),
				entry.UpdatedAt.Format(time.DateTime),
				entry.Runtime,
				expires,
				result.Path,
			); err != nil {
				return err
			}
			if err := writeRefs(w, entry.Refs); err != nil {
				return err
			}

			if _, err := fmt.Fprintln(w, "\nFiles:"); err != nil {
				return err
			}
			for _, file := range result.Files {
				if _, err := fmt.Fprintf(w, "  %s  %s\n", file.Path, journal.FormatSize(file.Size)); err != nil {
					return err
				}
			}

			if _, err := fmt.Fprintf(w, "\nNotes:\n%s\n", strings.TrimSpace(result.Notes)); err != nil {
				return err
			}

			events := result.Events
			if len(events) > maxShownEvents {
				events = events[len(events)-maxShownEvents:]
			}
			if _, err := fmt.Fprintln(w, "\nLog:"); err != nil {
				return err
			}
			for _, event := range events {
				line := fmt.Sprintf("  %s  %s by %s", event.At.Format(time.DateTime), event.Event, event.Runtime)
				if len(event.Summary) > 0 {
					line += ": " + event.Summary
				}
				if _, err := fmt.Fprintln(w, line); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func writeRefs(w io.Writer, refs journal.Refs) error {
	var parts []string
	for _, ref := range []struct {
		name  string
		value string
	}{
		{"branch", refs.Branch},
		{"commit", refs.Commit},
		{"pr", refs.PR},
		{"issue", refs.Issue},
	} {
		if len(ref.value) > 0 {
			parts = append(parts, ref.name+" "+ref.value)
		}
	}
	if len(parts) == 0 {
		return nil
	}

	_, err := fmt.Fprintf(w, "Refs:    %s\n", strings.Join(parts, ", "))
	return err
}

func ReportNote(output io.Writer, settings *journal.Settings, result *journal.EntryResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}
			_, err := fmt.Fprintf(w, "Recorded the update of journal entry %s; it is %s.\n",
				result.Entry.ID, result.Entry.Status)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportDone(output io.Writer, settings *journal.Settings, result *journal.EntryResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}
			expiry := "it is kept until deleted"
			if result.Entry.ExpiresAt != nil {
				expiry = "journal gc may delete it after " + result.Entry.ExpiresAt.Format(time.DateOnly)
			}
			_, err := fmt.Fprintf(w, "Marked journal entry %s %s; %s.\n", result.Entry.ID, result.Entry.Status, expiry)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportSearch(output io.Writer, settings *journal.Settings, result *journal.SearchResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}
			if len(result.Matches) == 0 {
				_, err := fmt.Fprintln(w, "No matching journal entries.")
				return err
			}

			for _, match := range result.Matches {
				if _, err := fmt.Fprintf(w, "%s  %-9s  %s%s\n",
					match.Entry.ID, match.Entry.Status, match.Entry.Title, formatTags(match.Entry.Tags),
				); err != nil {
					return err
				}
				for _, line := range match.Lines {
					if _, err := fmt.Fprintf(w, "    %s\n", line); err != nil {
						return err
					}
				}
			}
			return nil
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportIndex(output io.Writer, settings *journal.Settings, result *journal.IndexResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}
			_, err := fmt.Fprintf(w, "Indexed %d journal entries in %s.\n", result.Entries, result.Path)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportGC(output io.Writer, settings *journal.Settings, result *journal.GCResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if result == nil {
				return nil
			}

			for _, item := range result.Entries {
				if _, err := fmt.Fprintf(
					w,
					"%s  %-9s  expired %s  %s  %s\n",
					item.ID,
					item.Status,
					item.ExpiresAt.Format(time.DateOnly),
					journal.FormatSize(item.Size),
					item.Title,
				); err != nil {
					return err
				}
			}
			// After a partial gc, report what was removed before the error.
			if runErr != nil && len(result.Entries) == 0 {
				return nil
			}

			var err error
			switch {
			case len(result.Entries) == 0:
				_, err = fmt.Fprintln(w, "No expired journal entries.")
			case result.Applied:
				_, err = fmt.Fprintf(w, "Removed %d expired journal entries (%s).\n",
					len(result.Entries), journal.FormatSize(result.Size))
			default:
				_, err = fmt.Fprintf(
					w,
					"%d expired journal entries can be removed (%s); run with --apply to delete them.\n",
					len(result.Entries),
					journal.FormatSize(result.Size),
				)
			}
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return "  [" + strings.Join(tags, ", ") + "]"
}
