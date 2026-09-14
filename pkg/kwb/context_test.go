package kwb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContextDeletedPathRetainsDocumentationImpact(t *testing.T) {
	root, service := contextFixture(t)
	if err := os.Remove(filepath.Join(root, "src/auth.go")); err != nil {
		t.Fatal(err)
	}
	result, err := service.Context(
		t.Context(),
		ContextOptions{Task: "remove authentication", Paths: []string{"src/auth.go"}},
	)
	if err != nil || !hasDiagnostic(result, "source_unavailable") {
		t.Fatalf("deleted source: %+v %v", result, err)
	}
	for _, item := range result.Items {
		if item.Path == "docs/handbook/auth.md" && len(item.Evidence) > 0 {
			return
		}
	}
	t.Fatalf("deleted path lost documentation impact: %+v", result.Items)
}

func TestContextHistoricalSectionsRetainReplacementChain(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(
		t,
		root,
		"docs/decisions/old.md",
		"---\nid: ADR-old\ntitle: Old sessions\ncollection: decisions\nstatus: superseded\nsuperseded_by: ADR-current\ndate: 2026-09-01\n---\n# Old sessions\nPrevious session rules.\n",
	)
	writeFixture(
		t,
		root,
		"docs/decisions/current.md",
		"---\nid: ADR-current\ntitle: Current sessions\ncollection: decisions\nstatus: accepted\ndate: 2026-09-02\n---\n# Current sessions\nCurrent session rules.\n",
	)
	result, err := service.Context(
		t.Context(),
		ContextOptions{Task: "explain old sessions", Paths: []string{"docs/decisions/old.md"}, MaxBytes: 512},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Document != nil && item.Document.ID == "ADR-old" {
			if item.Document.Status != "superseded" || item.StatusMeaning == "" || len(item.Replacements) != 1 ||
				item.Replacements[0].ID != "ADR-current" {
				t.Fatalf("history metadata lost: %+v", item)
			}
			return
		}
	}
	t.Fatalf("historical document absent: %+v", result.Items)
}

func TestContextPreservesExactMethodsAndDistinctRanges(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(t, root, "src/service.go", "package sample\ntype Service struct{}\n"+
		"func (s *Service) Init() {\n println(\"initialize target\")\n}\n"+
		"func (s *Service) Refresh() {\n println(\"refresh target\")\n}\n")
	writeFixture(
		t,
		root,
		"src/other.go",
		"package sample\n// Service initialization refresh supporting material\nfunc Unrelated() {}\n",
	)
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	options := ContextOptions{Task: "Explain Service.Init and Service.Refresh"}
	result, err := service.Context(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"Init", "Refresh"} {
		found := false
		for _, item := range result.Items {
			if item.Path == "src/service.go" &&
				strings.Contains(item.Source.Content, "func (s *Service) "+method+"()") {
				found = true
				if item.Reason != "exact_symbol" || item.Query != "Service."+method {
					t.Fatalf("exact match provenance: %+v", item)
				}
			}
		}
		if !found {
			t.Fatalf("missing method %s: %+v", method, result.Items)
		}
	}
	for i, item := range result.Items {
		for _, previous := range result.Items[:i] {
			if item.Path == previous.Path && item.Source.StartLine <= previous.Source.EndLine &&
				previous.Source.StartLine <= item.Source.EndLine {
				t.Fatalf("overlapping evidence: %+v %+v", previous, item)
			}
		}
	}
	again, err := service.Context(t.Context(), options)
	if err != nil || !reflect.DeepEqual(result, again) {
		t.Fatalf("unstable context: %v", err)
	}
}

func TestContextReadsUnindexedExplicitFile(t *testing.T) {
	root, service := contextFixture(t)
	content := "mcp:\n  mise:\n    command: mise\n    args: [mcp]\n"
	writeFixture(t, root, "assets/defaults.custom", content)
	result, err := service.Context(
		t.Context(),
		ContextOptions{Task: "default mise MCP settings", Paths: []string{"assets/defaults.custom"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) < 2 || result.Items[1].Path != "assets/defaults.custom" ||
		result.Items[1].Source.Content != content || result.Items[1].Reason != "explicit_source" {
		t.Fatalf("explicit source absent: %+v", result.Items)
	}
	if !hasDiagnostic(result, "index_unavailable") {
		t.Fatal("missing index coverage not reported")
	}
}

func TestContextScopesDirectoryAtPathBoundary(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(t, root, "auth/target.go", "package auth\nfunc RotateToken() {}\n")
	writeFixture(t, root, "authentication/other.go", "package authentication\nfunc RotateToken() {}\n")
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	result, err := service.Context(t.Context(), ContextOptions{Task: "rotate token", Paths: []string{"auth/"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) < 2 || result.Items[1].Path != "auth/target.go" || result.Items[1].Reason != "scoped_search" {
		t.Fatalf("directory scope lost: %+v", result.Items)
	}
	for _, item := range result.Items {
		if item.Path == "authentication/other.go" && item.Reason == "scoped_search" {
			t.Fatalf("scope crossed path boundary: %+v", item)
		}
	}
}

func TestContextSelectsLocalDocumentationSectionsAndGuidance(t *testing.T) {
	root, service := contextFixture(t, func(s *Settings) { s.ContextDocs = []string{"conventions", "missing-rules"} })
	writeFixture(
		t,
		root,
		"docs/handbook/conventions.md",
		"---\nid: conventions\ntitle: Conventions\ncollection: handbook\nsidebar_position: 2\n---\n# Conventions\nAlways preserve request cancellation.\n",
	)
	writeFixture(
		t,
		root,
		"docs/handbook/api.md",
		"---\nid: api\ntitle: API\ncollection: handbook\nsidebar_position: 5\n---\n# API\nIntroduction only.\n\n## Session sequence\nRefresh tokens follow the session sequence.\n\n## Rotation and reuse\nRefresh tokens rotate; reuse revokes the session.\n",
	)
	result, err := service.Context(t.Context(), ContextOptions{Task: "refresh token session sequence rotation reuse"})
	if err != nil {
		t.Fatal(err)
	}
	guidance, sequence, rotation := false, false, false
	for _, item := range result.Items {
		guidance = guidance ||
			item.Reason == "project_guidance" && strings.Contains(item.Source.Content, "preserve request cancellation")
		if item.Path == "docs/handbook/api.md" {
			if item.Document == nil || item.Document.ID != "api" || !strings.Contains(item.Title, "API") {
				t.Fatalf("section metadata absent: %+v", item)
			}
			sequence = sequence || strings.Contains(item.Source.Content, "Refresh tokens follow")
			rotation = rotation || strings.Contains(item.Source.Content, "reuse revokes")
		}
	}
	if !guidance || !sequence || !rotation || !hasDiagnostic(result, "guidance_unavailable") {
		t.Fatalf("guidance=%v sequence=%v rotation=%v result=%+v", guidance, sequence, rotation, result)
	}
}

func TestContextReservesBudgetForRequestedEvidence(t *testing.T) {
	root, service := contextFixture(t, func(s *Settings) { s.ContextDocs = []string{"conventions"} })
	writeFixture(
		t,
		root,
		"docs/handbook/conventions.md",
		"---\nid: conventions\ntitle: Conventions\ncollection: handbook\nsidebar_position: 2\n---\n# Conventions\n"+strings.Repeat(
			"Follow project rules and keep callers compatible.\n",
			80,
		),
	)
	writeFixture(t, root, "src/target.go", "package target\nfunc RequestedMethod() {}\n")
	result, err := service.Context(
		t.Context(),
		ContextOptions{Task: "RequestedMethod", Paths: []string{"src/target.go"}, MaxBytes: 512},
	)
	if err != nil {
		t.Fatal(err)
	}
	found, continuation := false, false
	guidanceBytes := 0
	for _, item := range result.Items {
		found = found || strings.Contains(item.Source.Content, "func RequestedMethod()")
		if item.Reason == "bootstrap" || item.Reason == "project_guidance" {
			guidanceBytes += len(item.Source.Content)
			continuation = continuation || item.Source.NextStartLine != nil
		}
	}
	if !found || !continuation || guidanceBytes > 128 || result.ContentBytes > 512 ||
		!hasDiagnostic(result, "content_budget") {
		t.Fatalf("budget allocation: %+v", result)
	}
}
