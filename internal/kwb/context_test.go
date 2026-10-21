package kwb

import (
	"encoding/json"
	"errors"
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
	result, err := service.Context(t.Context(), ContextOptions{Paths: []string{"src/auth.go"}})
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

func TestContextHistoricalDocumentRetainsReplacementChain(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(t, root, "docs/decisions/old.md", record("ADR-old", "Old sessions",
		"collection: decisions\nstatus: superseded\nsuperseded_by: ADR-current\ndate: 2026-09-01\n",
		"# Old sessions\nPrevious session rules."))
	writeFixture(t, root, "docs/decisions/current.md", record("ADR-current", "Current sessions",
		"collection: decisions\nstatus: accepted\ndate: 2026-09-02\n",
		"# Current sessions\nCurrent session rules."))
	result, err := service.Context(t.Context(), ContextOptions{
		Paths:    []string{"docs/decisions/old.md"},
		MaxBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Document != nil && item.Document.ID == "ADR-old" {
			if item.Document.Status != "superseded" || len(item.StatusMeaning) == 0 || len(item.Replacements) != 1 ||
				item.Replacements[0].ID != "ADR-current" {
				t.Fatalf("history metadata lost: %+v", item)
			}
			return
		}
	}
	t.Fatalf("historical document absent: %+v", result.Items)
}

func TestContextDoesNotDiscoverSource(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(t, root, "src/service.go", "package sample\ntype Service struct{}\n"+
		"func (s *Service) Init() {}\nfunc (s *Service) Refresh() {}\n")
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	result, err := service.Context(t.Context(), ContextOptions{})
	if err != nil || len(result.Items) != 1 || result.Items[0].Reason != "bootstrap" || len(result.Diagnostics) != 0 {
		t.Fatalf("implicit evidence: %+v %v", result, err)
	}
	found, err := service.Search(t.Context(), SearchOptions{
		Query: "Service.Init",
		Kind:  "code",
	})
	if err != nil || len(found.Results) != 1 || found.Results[0].Path != "src/service.go" {
		t.Fatalf("explicit source search: %+v %v", found, err)
	}
}

func TestContextReadsLiveExplicitFile(t *testing.T) {
	root, service := contextFixture(t)
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"first configuration\n", "updated configuration\n"} {
		writeFixture(t, root, "assets/defaults.custom", content)
		result, err := service.Context(t.Context(), ContextOptions{Paths: []string{"assets/defaults.custom"}})
		if err != nil || len(result.Items) != 2 || len(result.Diagnostics) != 0 {
			t.Fatalf("live source: %+v %v", result, err)
		}
		item := result.Items[1]

		if item.Path != "assets/defaults.custom" || item.Source.Content != content ||
			item.Reason != "explicit_source" || item.Hash != Hash([]byte(content)) {
			t.Fatalf("live content or provenance lost: %+v", item)
		}
	}
}

func TestContextReadsUpstreamAsOrdinaryMarkdown(t *testing.T) {
	root, service := contextFixture(t)
	content := "---\ntitle: Upstream guide\n---\n# Upstream guide\n[Web](/wiki/Guide)\n"
	writeFixture(t, root, "upstream/guide.md", content)
	result, err := service.Context(t.Context(), ContextOptions{Paths: []string{"upstream/guide.md"}})
	if err != nil || len(result.Items) != 2 || len(result.Diagnostics) != 0 {
		t.Fatalf("upstream record validation: %+v %v", result, err)
	}
	item := result.Items[1]

	if item.Document != nil || len(item.StatusMeaning) > 0 || item.Source.Content != content {
		t.Fatalf("upstream was treated as a project record: %+v", item)
	}
}

func TestContextDirectorySelectsOnlyDocumentationImpact(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(t, root, "auth/target.go", "package auth\nfunc RotateToken() {}\n")
	writeFixture(t, root, "authentication/other.go", "package authentication\nfunc RotateToken() {}\n")
	writeFixture(t, root, "docs/auth.md", record("auth-guide", "Authentication", "",
		"# Authentication\n[Code](../auth/target.go)"))
	writeFixture(t, root, "docs/other.md", record("other", "Other", "",
		"# Other\n[Code](../authentication/other.go)"))
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	result, err := service.Context(t.Context(), ContextOptions{Paths: []string{"auth/"}})
	if err != nil || len(result.Items) != 2 || result.Items[1].Path != "docs/auth.md" ||
		result.Items[1].Reason != "direct_link" {
		t.Fatalf("directory documentation impact: %+v %v", result, err)
	}
	for _, item := range result.Items {
		if strings.HasSuffix(item.Path, ".go") || item.Path == "docs/other.md" {
			t.Fatalf("directory triggered discovery or crossed boundary: %+v", item)
		}
	}
}

func TestContextReadsRequestedDocumentationAndGuidance(t *testing.T) {
	root, service := contextFixture(t, func(s *Settings) {
		s.ContextDocs = []string{"conventions", "missing-rules"}
	})
	writeFixture(t, root, "docs/handbook/conventions.md", record("conventions", "Conventions",
		"collection: handbook\nsidebar_position: 2\n", "# Conventions\nPreserve request cancellation."))
	writeFixture(t, root, "docs/handbook/api.md", record(
		"api",
		"API",
		"collection: handbook\nsidebar_position: 5\n",
		"# API\nIntroduction.\n\n## Sessions\nTokens follow the session sequence.\n\n## Rotation\nReuse revokes the session.",
	))
	result, err := service.Context(t.Context(), ContextOptions{Paths: []string{"docs/handbook/api.md"}})
	if err != nil {
		t.Fatal(err)
	}
	guidance, requested := false, false
	for _, item := range result.Items {
		guidance = guidance ||
			item.Reason == "project_guidance" && strings.Contains(item.Source.Content, "cancellation")

		if item.Path == "docs/handbook/api.md" {
			requested = item.Document != nil && item.Document.ID == "api" &&
				strings.Contains(
					item.Source.Content,
					"session sequence",
				) && strings.Contains(item.Source.Content, "Reuse revokes")
		}
	}
	if !guidance || !requested || !hasDiagnostic(result, "guidance_unavailable") {
		t.Fatalf("guidance=%v requested=%v result=%+v", guidance, requested, result)
	}
}

func TestContextReservesBudgetForRequestedEvidence(t *testing.T) {
	root, service := contextFixture(t, func(s *Settings) {
		s.ContextDocs = []string{"conventions"}
	})
	writeFixture(t, root, "docs/handbook/conventions.md", record("conventions", "Conventions",
		"collection: handbook\nsidebar_position: 2\n",
		"# Conventions\n"+strings.Repeat("Follow project rules and preserve cancellation.\n", 80)))
	writeFixture(t, root, "src/target.go", "package target\nfunc RequestedMethod() {}\n")
	result, err := service.Context(t.Context(), ContextOptions{
		Paths:    []string{"src/target.go"},
		MaxBytes: 512,
	})
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

func TestContextDeduplicatesRequestedGuidance(t *testing.T) {
	_, service := contextFixture(t, func(s *Settings) {
		s.ContextDocs = []string{"conventions"}
	})
	result, err := service.Context(t.Context(), ContextOptions{
		Paths: []string{"docs/handbook/conventions.md", "docs/handbook/conventions.md"},
	})
	if err != nil || len(result.Items) != 2 || result.Items[1].Reason != "project_guidance" ||
		len(result.Items[1].Evidence) == 0 {
		t.Fatalf("duplicate guidance or lost impact evidence: %+v %v", result, err)
	}
}

func TestContextReturnsRequestedTemplate(t *testing.T) {
	_, service := contextFixture(t)
	result, err := service.Context(t.Context(), ContextOptions{Paths: []string{"docs/templates/requirement.md"}})
	if err != nil || len(result.Items) != 2 || result.Items[1].Document == nil ||
		result.Items[1].Document.Collection != "templates" || result.Items[1].Reason != "explicit_source" {
		t.Fatalf("requested authoring template: %+v %v", result, err)
	}
}

func TestContextBudgetsAndPagination(t *testing.T) {
	root, service := contextFixture(t)
	writeFixture(t, root, "src/large.go", "\npackage sample\n"+strings.Repeat("// source line\n", 600))
	for _, budget := range []int{1, 100, 1024, DefaultContentBytes, MaxContentBytes} {
		result, err := service.Context(t.Context(), ContextOptions{
			Paths:    []string{"src/large.go"},
			MaxBytes: budget,
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		budgetDiagnostics := 0
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Code == "content_budget" {
				budgetDiagnostics++
			}
		}
		if result.ContentBytes > budget || len(data) > MaxResponseBytes || !result.Truncated || budgetDiagnostics > 1 {
			t.Fatalf("budget=%d result=%+v encoded=%d", budget, result, len(data))
		}
		for _, item := range result.Items {
			if len(strings.TrimSpace(item.Source.Content)) == 0 {
				t.Fatalf("blank excerpt for budget %d: %+v", budget, item)
			}
			if item.Path == "src/large.go" && (item.Source.NextStartLine == nil ||
				*item.Source.NextStartLine != item.Source.EndLine+1 || item.Source.EndLine > 200) {
				t.Fatalf("continuation lost: %+v", item)
			}
		}
	}
}

func TestContextDeterminism(t *testing.T) {
	_, service := contextFixture(t)
	options := ContextOptions{Paths: []string{"src/auth.go", "docs/handbook/auth.md"}}
	first, err := service.Context(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Context(t.Context(), options)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable context: first=%+v second=%+v error=%v", first, second, err)
	}
}

func TestContextUsesConfiguredEntrypoint(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "team/start.markdown", "# Start here\n")
	writeFixture(t, root, "docs/README.md", "---\nid: unselected\ntitle: Unselected\n---\n# Unselected\n")
	writeFixture(t, root, ".go42x/go42x.yaml", `version: "1.0"
project: {name: example, documentation-entrypoint: team/start.markdown}
context: {template: agents.tpl.md}
providers: {codex: {enabled: false}}
`)
	service := testService(t, root, filepath.Join(root, ".go42x/kwb/index"), nil)
	result, err := service.Context(t.Context(), ContextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Path != "team/start.markdown" ||
		result.Items[0].Reason != "bootstrap" ||
		result.Items[0].Source.Content != "# Start here\n" {
		t.Fatalf("startup context: %+v", result.Items)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("plain Markdown entrypoint was rejected: %+v", result.Diagnostics)
	}
}

func TestContextOrderStatusSourceAndDeterminism(t *testing.T) {
	root, service := contextFixture(t)
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	options := ContextOptions{
		Paths: []string{"src/auth.go"},
	}
	result, err := service.Context(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 5 || result.Items[0].Path != "docs/README.md" || result.Items[1].Path != "src/auth.go" {
		t.Fatalf("items: %+v", result.Items)
	}
	foundCode, foundDraft, foundLink := false, false, false
	for _, item := range result.Items {
		if item.Path == "docs/handbook/auth.md" && item.Reason == "direct_link" && len(item.Evidence) > 0 {
			foundLink = true
		}
		if item.Path == "src/auth.go" {
			foundCode = true
		}
		if item.Document != nil && item.Document.Status == "draft" {
			foundDraft = strings.Contains(item.StatusMeaning, "Not accepted")
		}
		if item.Document != nil && item.Document.Collection == "templates" {
			t.Fatal("template in implementation context")
		}
	}
	if !foundCode || !foundDraft || !foundLink {
		t.Fatalf("code=%v draft=%v link=%v", foundCode, foundDraft, foundLink)
	}
	second, err := service.Context(t.Context(), options)
	if err != nil || !reflect.DeepEqual(result, second) {
		t.Fatalf("nondeterministic: %v", err)
	}
}

func TestContextRejectsMixedRoots(t *testing.T) {
	root, indexed := contextFixture(t)
	if _, err := indexed.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	service := testService(t, t.TempDir(), indexed.settings.IndexPath, func(settings *Settings) {
		settings.RequireRootMatch = true
	})
	if _, err := service.Context(t.Context(), ContextOptions{}); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("mixed roots: %v", err)
	}
}

func TestConfiguredDefaultContentBudget(t *testing.T) {
	const budget = 64
	_, service := contextFixture(t, func(settings *Settings) {
		settings.DefaultContentBytes = budget
	})
	result, err := service.Context(t.Context(), ContextOptions{
		Paths: []string{"src/auth.go"},
	})
	if err != nil || result.ContentBytes > budget || !result.Truncated || hasDiagnostic(result, "index_unavailable") {
		t.Fatalf("configured default without an index: %+v %v", result, err)
	}
	result, err = service.Context(t.Context(), ContextOptions{
		Paths:    []string{"src/auth.go"},
		MaxBytes: DefaultContentBytes,
	})
	if err != nil || result.ContentBytes <= budget || result.ContentBytes > DefaultContentBytes {
		t.Fatalf("explicit request budget: %+v %v", result, err)
	}
}
