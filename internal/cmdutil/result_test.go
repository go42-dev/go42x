package cmdutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestReportJSONFailurePreservesPartialResult(t *testing.T) {
	var output bytes.Buffer
	failure := errors.New("update interrupted")
	partial := map[string]any{
		"backup_dir": "recovery",
		"applied":    2,
	}
	err := Report(&output, partial, failure, nil)
	var exit *ExitError
	if !errors.Is(err, failure) || !errors.As(err, &exit) || !exit.Reported || ExitCode(err) != 1 {
		t.Fatalf("unexpected failure: %v", err)
	}
	var result struct {
		Error  string         `json:"error"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != failure.Error() || result.Result["backup_dir"] != "recovery" ||
		result.Result["applied"] != float64(2) {
		t.Fatalf("lost error or recovery information: %+v", result)
	}
}

func TestReportPreservesDiagnosticJSON(t *testing.T) {
	var output bytes.Buffer
	failure := ReportedError(errors.New("checks failed"))
	result := map[string]any{
		"status": "fail",
		"checks": []string{"configuration"},
	}
	if err := Report(&output, result, failure, nil); err != failure {
		t.Fatalf("unexpected failure: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["status"] != "fail" || decoded["error"] != nil || decoded["result"] != nil {
		t.Fatalf("diagnostic result was wrapped: %v", decoded)
	}
}

func TestReportTextFailureLeavesErrorForCobra(t *testing.T) {
	var output bytes.Buffer
	failure := errors.New("update interrupted")
	err := Report(&output, nil, failure, func(w io.Writer) error {
		_, err := io.WriteString(w, "Backup: recovery\n")
		return err
	})
	if err != failure || output.String() != "Backup: recovery\n" {
		t.Fatalf("text result or error changed: %q, %v", output.String(), err)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestReportWriteFailureRemainsUnreported(t *testing.T) {
	failure := errors.New("output unavailable")
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var text func(io.Writer) error
			if format == "text" {
				text = func(w io.Writer) error {
					_, err := io.WriteString(w, "failed\n")
					return err
				}
			}
			err := Report(failingWriter{failure}, "failed", ReportedError(errors.New("checks failed")), text)
			var exit *ExitError
			if !errors.Is(err, failure) || (errors.As(err, &exit) && exit.Reported) || ExitCode(err) != 1 {
				t.Fatalf("output failure was hidden: %v", err)
			}
		})
	}
}
