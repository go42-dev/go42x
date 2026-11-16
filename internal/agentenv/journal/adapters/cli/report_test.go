package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
)

var (
	textSettings = &journal.Settings{}
	jsonSettings = &journal.Settings{JSON: true}
	updated      = time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
)

func testEntry() *journal.Entry {
	return &journal.Entry{
		ID:        "20261110-task-0000",
		Title:     "Task",
		Status:    journal.StatusOpen,
		Tags:      []string{"fix", "kwb"},
		CreatedAt: updated,
		UpdatedAt: updated,
		Runtime:   "codex",
	}
}

func TestReportList(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		result *journal.ListResult
		stray  bool
		want   string
	}{
		"entries": {
			result: &journal.ListResult{
				Entries: []*journal.Entry{testEntry()},
				Invalid: []journal.Problem{{Path: ".go42x/journal/20261110-broken-0000", Error: "parse entry.json"}},
			},
			want: "Invalid entry .go42x/journal/20261110-broken-0000: parse entry.json\n" +
				"20261110-task-0000  open       2026-11-10 12:30:00  Task  [fix, kwb]\n",
		},
		"no entries": {
			result: &journal.ListResult{},
			want:   "No journal entries.\n",
		},
		"stray": {
			result: &journal.ListResult{
				Stray: []journal.StrayItem{{Path: ".build/old-task", Size: 2048, ModifiedAt: updated}},
			},
			stray: true,
			want: ".build/old-task  2.0 KiB  2026-11-10\n" +
				"1 items without a journal record (2.0 KiB); review them and delete what is no longer needed. " +
				"Agents ask the user first.\n",
		},
		"no stray": {
			result: &journal.ListResult{},
			stray:  true,
			want:   "No content without a journal record.\n",
		},
	} {
		var output bytes.Buffer
		if err := ReportList(&output, textSettings, tt.result, tt.stray, nil); err != nil {
			t.Fatal(err)
		}
		if output.String() != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", name, output.String(), tt.want)
		}
	}
}

func TestReportShowKeepsTheLatestEvents(t *testing.T) {
	t.Parallel()
	entry := testEntry()
	entry.Refs = journal.Refs{Branch: "main", PR: "12"}
	events := make([]journal.Event, maxShownEvents+2)
	for i := range events {
		events[i] = journal.Event{At: updated, Event: fmt.Sprintf("event-%d", i), Runtime: "codex"}
	}
	events[len(events)-1].Summary = "Last"

	var output bytes.Buffer
	if err := ReportShow(&output, textSettings, &journal.ShowResult{
		Entry:  entry,
		Path:   ".go42x/journal/" + entry.ID,
		Notes:  "# Task\n",
		Events: events,
		Files:  []journal.EntryFile{{Path: ".go42x/journal/" + entry.ID + "/notes.md", Size: 7}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Expires: never\n",
		"Refs:    branch main, pr 12\n",
		"  .go42x/journal/20261110-task-0000/notes.md  7 B\n",
		"Notes:\n# Task\n",
		"event-2 by codex\n",
		"event-11 by codex: Last\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "event-0 ") || strings.Contains(text, "event-1 ") {
		t.Errorf("show printed more than %d events:\n%s", maxShownEvents, text)
	}
}

func TestReportDone(t *testing.T) {
	t.Parallel()
	kept := testEntry()
	kept.Status = journal.StatusDone
	expiring := testEntry()
	expiring.Status = journal.StatusAbandoned
	expires := updated.Add(journal.AbandonedRetention)
	expiring.ExpiresAt = &expires
	for entry, want := range map[*journal.Entry]string{
		kept:     "Marked journal entry 20261110-task-0000 done; it is kept until deleted.\n",
		expiring: "Marked journal entry 20261110-task-0000 abandoned; journal gc may delete it after 2026-11-17.\n",
	} {
		var output bytes.Buffer
		if err := ReportDone(&output, textSettings, &journal.EntryResult{Entry: entry}, nil); err != nil {
			t.Fatal(err)
		}
		if output.String() != want {
			t.Errorf("done = %q, want %q", output.String(), want)
		}
	}
}

func TestReportGC(t *testing.T) {
	t.Parallel()
	item := journal.GCItem{
		ID:        "20261110-task-0000",
		Title:     "Task",
		Status:    journal.StatusDone,
		ExpiresAt: updated,
		Size:      1024,
	}
	line := "20261110-task-0000  done       expired 2026-11-10  1.0 KiB  Task\n"
	failure := errors.New("remove journal entry")
	for name, tt := range map[string]struct {
		result *journal.GCResult
		runErr error
		want   string
	}{
		"nothing expired": {
			result: &journal.GCResult{},
			want:   "No expired journal entries.\n",
		},
		"dry run": {
			result: &journal.GCResult{Entries: []journal.GCItem{item}, Size: 1024},
			want:   line + "1 expired journal entries can be removed (1.0 KiB); run with --apply to delete them.\n",
		},
		"applied": {
			result: &journal.GCResult{Applied: true, Entries: []journal.GCItem{item}, Size: 1024},
			want:   line + "Removed 1 expired journal entries (1.0 KiB).\n",
		},
		"partly applied": {
			result: &journal.GCResult{Applied: true, Entries: []journal.GCItem{item}, Size: 1024},
			runErr: failure,
			want:   line + "Removed 1 expired journal entries (1.0 KiB).\n",
		},
		"nothing applied": {
			result: &journal.GCResult{Applied: true},
			runErr: failure,
		},
		"failed": {
			runErr: failure,
		},
	} {
		var output bytes.Buffer
		if err := ReportGC(&output, textSettings, tt.result, tt.runErr); !errors.Is(err, tt.runErr) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.runErr)
		}
		if output.String() != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", name, output.String(), tt.want)
		}
	}
}

func TestReportJSON(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := ReportList(
		&output,
		jsonSettings,
		&journal.ListResult{Entries: []*journal.Entry{}, Invalid: []journal.Problem{}},
		false,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(output.String()); got != `{"entries":[],"invalid":[]}` {
		t.Errorf("list = %s", got)
	}

	output.Reset()
	failure := errors.New("journal lock unavailable")
	if err := ReportNew(&output, jsonSettings, nil, failure); !errors.Is(err, failure) {
		t.Errorf("new error = %v, want %v", err, failure)
	}
	var reported struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &reported); err != nil || reported.Error != failure.Error() {
		t.Errorf("failure = %s, %v", output.String(), err)
	}
}
