package check

import (
	"context"
	"reflect"
	"testing"
)

func TestDependenciesAndIndependentChecks(t *testing.T) {
	var ran []string
	check := func(id string, status Status, deps ...string) Check {
		return Check{
			ID:        id,
			DependsOn: deps,
			Run:       func(context.Context) Result { ran = append(ran, id); return Result{Status: status} },
		}
	}
	report, err := Run(
		t.Context(),
		check("deploy", Pass, "clone"),
		check("clone", Fail),
		check("config", Warn),
		check("index", Pass, "config"),
	)
	if err != nil || report.Status != Fail || !reflect.DeepEqual(ran, []string{"clone", "config", "index"}) ||
		report.Checks[1].Status != Skipped {
		t.Fatalf("report=%+v ran=%v err=%v", report, ran, err)
	}
}

func TestInvalidGraphNeverExecutes(t *testing.T) {
	called := false
	check := Check{ID: "a", Run: func(context.Context) Result { called = true; return Result{Status: Pass} }}
	for _, checks := range [][]Check{{check, check}, {check, {ID: "b", DependsOn: []string{"missing"}, Run: check.Run}}, {{ID: "a", DependsOn: []string{"b"}, Run: check.Run}, {ID: "b", DependsOn: []string{"a"}, Run: check.Run}}} {
		if _, err := Run(t.Context(), checks...); err == nil || called {
			t.Fatalf("invalid graph executed: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, check); err == nil || called {
		t.Fatal("canceled check executed")
	}
}
