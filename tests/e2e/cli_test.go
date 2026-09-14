package e2e_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestHelpVersionAndUsage(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	before := p.snapshot(t)
	help := p.run(t, 0, "--help")
	for _, name := range []string{"agentenv", "doctor", "kwb", "mcp", "version"} {
		if !strings.Contains(help.stdout, "\n  "+name+" ") {
			t.Errorf("help does not list %s: %s", name, help.stdout)
		}
	}
	version := p.run(t, 0, "version")
	for _, want := range []string{
		"Version: " + binaryVersion + "\n", "Go:      go", "OS/Arch: " + runtime.GOOS + "/" + runtime.GOARCH,
	} {
		if !strings.Contains(version.stdout, want) {
			t.Errorf("version output %q does not contain %q", version.stdout, want)
		}
	}
	if version.stderr != "" || help.stderr != "" {
		t.Errorf("help/version wrote unexpected stderr: %q, %q", help.stderr, version.stderr)
	}
	for _, args := range [][]string{
		{"version", "unexpected"},
		{"version", "--unknown-flag"},
		{"version", "--log-level=invalid"},
		{"doctor", "--timeout=0"},
		{"mcp", "--search-timeout=0"},
	} {
		result := p.run(t, 2, args...)
		if result.stdout != "" || !strings.Contains(result.stderr, "Error:") {
			t.Errorf("invalid usage %v: stdout=%q stderr=%q", args, result.stdout, result.stderr)
		}
	}
	p.assertUnchanged(t, before)
}

type doctorReport struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Checks        []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"checks"`
}

func TestDoctorReportsAndExitCodes(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.run(t, 0, "agentenv", "generate")
	p.run(t, 0, "kwb", "build")
	// Run outside the project so --root must select the requested directory.
	caller := newProject(t)
	for _, tc := range []struct {
		name, status, configStatus string
		exit                       int
	}{
		{"healthy", "pass", "pass", 0},
		{"outdated instructions", "warn", "pass", 0},
		{"invalid configuration", "fail", "fail", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switch tc.name {
			case "outdated instructions":
				p.write(t, ".go42x/agents.tpl.md", "Updated instructions\n")
			case "invalid configuration":
				p.write(t, ".go42x/go42x.yaml", "version: [")
			}
			before := p.snapshot(t)
			result := caller.run(t, tc.exit, "doctor", "--root", p.root, "--json", "--log-level=debug")
			var report doctorReport
			if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
				t.Fatalf("doctor stdout is not a single JSON report: %v\n%s", err, result.stdout)
			}
			if report.SchemaVersion != 1 || report.Status != tc.status {
				t.Fatalf("doctor report = %+v, want schema 1 and status %s", report, tc.status)
			}
			statuses := make(map[string]string)
			for _, check := range report.Checks {
				statuses[check.ID] = check.Status
			}
			if statuses["agentenv.config"] != tc.configStatus || statuses["kwb.index"] != "pass" {
				t.Errorf("unexpected diagnostic checks: %v", statuses)
			}
			if strings.Contains(result.stderr, "Error:") {
				t.Errorf("doctor printed a duplicate error after its report: %q", result.stderr)
			}
			text := caller.run(t, tc.exit, "doctor", "--root", p.root)
			if !strings.Contains(text.stdout, tc.configStatus+" agentenv.config:") {
				t.Errorf("missing diagnostic in text output: %s", text.stdout)
			}
			p.assertUnchanged(t, before)
		})
	}
}
