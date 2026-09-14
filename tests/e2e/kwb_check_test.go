package e2e_test

import (
	"encoding/json"
	"testing"

	"github.com/go42-dev/go42x/pkg/kwb"
)

func TestKnowledgeBaseFreshnessCLI(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	assertStatus := func(want string, code int) kwb.FreshnessResult {
		t.Helper()
		output := p.run(t, code, "kwb", "check", "--json")
		var result kwb.FreshnessResult
		if err := json.Unmarshal(
			[]byte(output.stdout),
			&result,
		); err != nil || result.Status != want ||
			output.stderr != "" {
			t.Fatalf("freshness status %s: %+v %v", want, output, err)
		}
		return result
	}
	before := p.snapshot(t)
	assertStatus("missing", 1)
	p.assertUnchanged(t, before)
	p.write(t, "source.xyz", "custom source\n")
	p.run(t, 0, "kwb", "build", "--include-ext=.xyz")
	fresh := assertStatus("fresh", 0)
	p.write(t, "source.xyz", "modified custom source\n")
	stale := assertStatus("stale", 1)
	if stale.Changed.Count != 1 || stale.Generation != fresh.Generation {
		t.Fatalf("check changed generation or ignored custom source: %+v", stale)
	}
	p.run(t, 0, "kwb", "build", "--include-ext=.xyz")
	assertStatus("fresh", 0)
}
