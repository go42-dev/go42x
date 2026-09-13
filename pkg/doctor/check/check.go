// Package check defines diagnostic checks and runs their dependency graph.
package check

import (
	"context"
	"fmt"
)

type Status string

const (
	Pass    Status = "pass"
	Warn    Status = "warn"
	Fail    Status = "fail"
	Skipped Status = "skipped"
)

type Result struct {
	Children    []Result `json:"checks,omitempty"`
	ID          string   `json:"id"`
	Status      Status   `json:"status"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation,omitempty"`
	Evidence    []string `json:"evidence,omitempty"`
}

// Check owns its diagnostic logic. Dependencies name checks that must pass or
// warn before Run can execute. Checks must honor cancellation and avoid writes.
// An explicitly requested connection probe may start external processes.
type Check struct {
	ID        string
	DependsOn []string
	Run       func(context.Context) Result
}

type Report struct {
	SchemaVersion int      `json:"schema_version"`
	Status        Status   `json:"status"`
	Checks        []Result `json:"checks"`
}

// Run validates the entire dependency graph before executing any check. Results
// are deterministic: dependencies first, otherwise in registration order.
// Failed checks skip their dependents; unrelated checks continue.
func Run(ctx context.Context, checks ...Check) (*Report, error) {
	byID := make(map[string]Check, len(checks))

	for _, check := range checks {
		if check.ID == "" || check.Run == nil {
			return nil, fmt.Errorf("check ID and Run are required")
		}
		if _, exists := byID[check.ID]; exists {
			return nil, fmt.Errorf("duplicate check %q", check.ID)
		}
		byID[check.ID] = check
	}

	state := make(map[string]int)

	var ordered []Check
	var visit func(string) error

	visit = func(id string) error {
		if state[id] == 2 {
			return nil
		}
		if state[id] == 1 {
			return fmt.Errorf("check dependency cycle at %q", id)
		}
		check, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown check dependency %q", id)
		}
		state[id] = 1
		for _, dependency := range check.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		ordered = append(ordered, check)
		return nil
	}

	for _, check := range checks {
		if err := visit(check.ID); err != nil {
			return nil, err
		}
	}

	report := &Report{SchemaVersion: 1, Status: Pass, Checks: []Result{}}
	results := make(map[string]Status)

	for _, check := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := Result{Status: Skipped}
		for _, dependency := range check.DependsOn {
			if results[dependency] == Fail || results[dependency] == Skipped {
				result.Message = "Dependency unavailable: " + dependency
				break
			}
		}
		if result.Message == "" {
			result = check.Run(ctx)
		}
		result.ID = check.ID
		switch result.Status {
		case Fail:
			report.Status = Fail
		case Warn:
			if report.Status == Pass {
				report.Status = Warn
			}
		case Pass, Skipped:
		default:
			return nil, fmt.Errorf("check %q returned invalid status %q", check.ID, result.Status)
		}
		results[check.ID] = result.Status
		report.Checks = append(report.Checks, result)
	}

	return report, ctx.Err()
}
