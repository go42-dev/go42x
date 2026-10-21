package kwb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

func newDocs(t *testing.T, root string) *Service {
	t.Helper()
	return testService(t, root, filepath.Join(root, ".go42x/kwb/index"), nil)
}

func record(id, title, extra, body string) string {
	return fmt.Sprintf("---\nid: %s\ntitle: %s\n%s---\n\n%s\n", id, title, extra, body)
}

func TestCatalogProjectConfiguration(t *testing.T) {
	const shared = `version: "1.0"
project: {name: example, documentation-entrypoint: handbook/shared.md}
context: {template: agents.tpl.md}
providers: {codex: {enabled: false}}
`
	for _, tt := range []struct {
		name, config, local, entrypoint string
	}{
		{
			"no configuration",
			"",
			"",
			"docs/README.md",
		},
		{
			"shared",
			shared,
			"",
			"handbook/shared.md",
		},
		{
			"repository root",
			strings.ReplaceAll(shared, "handbook/shared.md", "README.md"),
			"",
			"README.md",
		},
		{
			"local override",
			shared,
			"project: {documentation-entrypoint: team/local.markdown}",
			"team/local.markdown",
		},
		{
			"local clear",
			shared,
			"project: {documentation-entrypoint: null}",
			"docs/README.md",
		},
		{
			"normalized",
			shared,
			"project: {documentation-entrypoint: ./team/../handbook/shared.md}",
			"handbook/shared.md",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			if len(tt.config) > 0 {
				writeFixture(t, root, ".go42x/go42x.yaml", tt.config)
			}
			if len(tt.local) > 0 {
				writeFixture(t, root, ".go42x/go42x.local.yaml", tt.local)
			}
			writeFixture(t, root, tt.entrypoint, "# Project documentation\n")
			catalog, err := newDocumentCatalog().Load(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if catalog.entrypoint != tt.entrypoint || catalog.ByPath(tt.entrypoint) == nil ||
				len(catalog.Diagnostics) != 0 {
				t.Fatalf("catalog uses wrong documentation entrypoint: %+v", catalog)
			}
			// Local configuration changes take effect without restarting the service.
			writeFixture(t, root, ".go42x/go42x.yaml", shared)
			writeFixture(t, root, ".go42x/go42x.local.yaml", "project: {documentation-entrypoint: team/updated.md}")
			writeFixture(t, root, "team/updated.md", "# Updated documentation\n")
			writeFixture(t, root, "team/guide.md", record("team-guide", "Team guide", "", "# Team guide"))
			service := newDocs(t, root)
			first, err := service.Catalog(t.Context())
			if err != nil || first.entrypoint != "team/updated.md" || first.ByPath("team/guide.md") == nil {
				t.Fatalf("updated entrypoint = %+v, %v", first, err)
			}
			writeFixture(t, root, ".go42x/go42x.local.yaml", "project: {documentation-entrypoint: handbook/shared.md}")
			writeFixture(t, root, "handbook/shared.md", "# Shared documentation\n")
			second, err := service.Catalog(t.Context())
			if err != nil || second.entrypoint != "handbook/shared.md" || second.ByPath("team/updated.md") != nil ||
				second.ByPath("team/guide.md") != nil {
				t.Fatalf("stale entrypoint after local override changed = %+v, %v", second, err)
			}
		})
	}
}

func TestCatalogRejectsInvalidProjectConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name, shared, local string
	}{
		{
			"malformed shared",
			"project: [",
			"",
		},
		{
			"invalid shared",
			"version: '1.0'\n",
			"",
		},
		{
			"malformed local",
			"version: '1.0'\n",
			"project: [",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, ".go42x/go42x.yaml", tt.shared)
			if len(tt.local) > 0 {
				writeFixture(t, root, ".go42x/go42x.local.yaml", tt.local)
			}
			catalog, err := newDocumentCatalog().Load(t.Context(), root)
			if catalog != nil || err == nil || !strings.Contains(err.Error(), "load documentation configuration:") {
				t.Fatalf("invalid configuration fell back to defaults: catalog=%+v error=%v", catalog, err)
			}
		})
	}
}

func TestCatalogExcludesUpstreamMarkdown(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "docs/README.md", "# Documentation\n[Upstream](../upstream/guide.md)\n")
	writeFixture(t, root, "src/auth.go", "package auth\n")
	writeFixture(t, root, "docs/auth.md", record("auth", "Auth", "", "# Auth\n[Code](../src/auth.go)"))
	writeFixture(
		t,
		root,
		"upstream/guide.md",
		"---\ntitle: Upstream guide\n---\n# Upstream guide\n[Web](/wiki/Guide)\n",
	)
	writeFixture(t, root, "upstream/copy.md", record("auth", "Upstream copy", "",
		"# upstreamneedle\n[Code](../src/auth.go)"))
	writeFixture(t, root, "upstream/malformed.md", "---\ntitle: [\n---\n")
	service := newDocs(t, root)
	catalog, err := service.Catalog(t.Context())
	if err != nil || len(catalog.Documents) != 2 || len(catalog.Diagnostics) != 0 {
		t.Fatalf("upstream entered project catalog: %+v %v", catalog, err)
	}
	doc, err := catalog.ByID("auth")
	if err != nil || doc.Path != "docs/auth.md" || catalog.ByPath("upstream/guide.md") != nil {
		t.Fatalf("upstream ID or path promoted: %+v %v", doc, err)
	}
	impact, err := service.Impact(t.Context(), []string{"src/auth.go"})
	if err != nil || len(impact.Documents) != 1 || impact.Documents[0].Path != "docs/auth.md" {
		t.Fatalf("upstream became project guidance: %+v %v", impact, err)
	}
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	found, err := service.Search(t.Context(), SearchOptions{
		Query:      "upstreamneedle",
		PathPrefix: "upstream/",
	})
	if err != nil || len(found.Results) != 1 || found.Results[0].Path != "upstream/copy.md" {
		t.Fatalf("upstream is no longer searchable: %+v %v", found, err)
	}
}

func TestCatalogValidatesOnlyDocumentationTree(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "docs/README.md", "# Documentation\n")
	writeFixture(t, root, "docs/missing-id.md", "---\ntitle: Project page\n---\n# Project page\n")
	writeFixture(t, root, "upstream/missing-id.md", "---\ntitle: Upstream page\n---\n# Upstream page\n")
	catalog, err := newDocumentCatalog().Load(t.Context(), root)
	if err != nil || len(catalog.Diagnostics) != 1 || catalog.Diagnostics[0].Code != "invalid_metadata" ||
		catalog.Diagnostics[0].Path != "docs/missing-id.md" {
		t.Fatalf("metadata validation escaped documentation tree: %+v %v", catalog, err)
	}
}

func TestCatalogNestedDirectoryBoundary(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".go42x/go42x.yaml", `version: "1.0"
project: {name: example, documentation-entrypoint: team/docs/start.md}
context: {template: agents.tpl.md}
providers: {codex: {enabled: false}}
`)
	writeFixture(t, root, "team/docs/start.md", "# Documentation\n")
	writeFixture(t, root, "team/docs/nested/guide.md", record("inside", "Inside", "", "# Inside"))
	writeFixture(t, root, "team/ancestor.md", record("ancestor", "Ancestor", "", "# Ancestor"))
	writeFixture(t, root, "team/docs-other/guide.md", record("sibling", "Sibling", "", "# Sibling"))
	writeFixture(t, root, ".gitignore", "team/docs/root-ignored.md\n")
	writeFixture(t, root, "team/.gitignore", "docs/ancestor-ignored.md\n")
	writeFixture(t, root, "team/docs/nested/.gitignore", "nested-ignored.md\n")
	for _, name := range []string{"root-ignored.md", "ancestor-ignored.md", "nested/nested-ignored.md"} {
		writeFixture(t, root, "team/docs/"+name, record(name, name, "", "# Ignored"))
	}
	catalog, err := newDocumentCatalog().Load(t.Context(), root)
	if err != nil || len(catalog.Documents) != 2 || len(catalog.Diagnostics) != 0 {
		t.Fatalf("nested documentation boundary or ancestor ignores lost: %+v %v", catalog, err)
	}
	if _, err := catalog.ByID("inside"); err != nil {
		t.Fatal(err)
	}
}

func TestMarkdownFrontmatterTablesReferencesAndFences(t *testing.T) {
	source := "---\nid: guide\ntitle: Guide\nrelated:\n  - REQ-001\n---\n\n# Real heading\n\n| Source | Purpose |\n| --- | --- |\n| [auth][impl] | Tokens |\n\n[impl]: ../../src/auth.go#L7\n\n```md\n# Fake heading\n[bad](../../secret)\n```\n\nSecond heading\n--------------\n"
	doc, err := Parse("docs/handbook/auth.md", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Headings) != 2 || doc.Headings[0].Title != "Real heading" || doc.Headings[0].StartLine != 8 ||
		doc.Headings[1].StartLine != 21 {
		t.Fatalf("headings: %+v", doc.Headings)
	}
	if len(doc.Links) != 1 || doc.Links[0].Path != "src/auth.go" || doc.Links[0].Line != 12 ||
		doc.Links[0].Fragment != "L7" {
		t.Fatalf("links: %+v", doc.Links)
	}
	if doc.BodyStartLine != 7 {
		t.Fatalf("body: %d", doc.BodyStartLine)
	}
}

func TestLiveGetStatusAmbiguityAndSupersession(t *testing.T) {
	root := t.TempDir()
	s := newDocs(t, root)
	oldPath := "docs/decisions/001-old.md"
	newPath := "docs/decisions/002-new.md"
	writeFixture(
		t,
		root,
		oldPath,
		record(
			"ADR-001",
			"Old",
			"collection: decisions\nstatus: superseded\ndate: 2026-09-01\nsuperseded_by: ADR-002\n",
			"# Original reasoning",
		),
	)
	writeFixture(
		t,
		root,
		newPath,
		record("ADR-002", "Current", "collection: decisions\nstatus: accepted\ndate: 2026-09-02\n", "# New reasoning"),
	)
	result, err := s.GetDocument(t.Context(), "ADR-001", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Document.ID != "ADR-001" || result.Document.Status != "superseded" || len(result.Replacements) != 1 ||
		!strings.Contains(result.Source.Content, "Original reasoning") {
		t.Fatalf("%+v", result)
	}
	oldHash := result.Document.Hash
	writeFixture(
		t,
		root,
		oldPath,
		record(
			"ADR-001",
			"Old",
			"collection: decisions\nstatus: superseded\ndate: 2026-09-01\nsuperseded_by: ADR-002\n",
			"# Updated original reasoning",
		),
	)
	result, err = s.GetDocument(t.Context(), "ADR-001", 0, 0)
	if err != nil || oldHash == result.Document.Hash || !strings.Contains(result.Source.Content, "Updated") {
		t.Fatalf("live: %+v %v", result, err)
	}
	writeFixture(
		t,
		root,
		"docs/decisions/003-copy.md",
		record("ADR-001", "Copy", "collection: decisions\nstatus: proposed\ndate: 2026-09-03\n", "# Copy"),
	)
	if _, err = s.GetDocument(t.Context(), "ADR-001", 0, 0); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate: %v", err)
	}
	if err = os.Remove(filepath.Join(root, "docs/decisions/003-copy.md")); err != nil {
		t.Fatal(err)
	}
	writeFixture(
		t,
		root,
		newPath,
		record(
			"ADR-002",
			"Cycle",
			"collection: decisions\nstatus: superseded\ndate: 2026-09-02\nsuperseded_by: ADR-001\n",
			"# Cycle",
		),
	)
	result, err = s.GetDocument(t.Context(), "ADR-001", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) == 0 {
		t.Fatal("cycle not diagnosed")
	}
}

func TestImpactEvidenceDeletedPathsAndCycles(t *testing.T) {
	root := t.TempDir()
	s := newDocs(t, root)
	writeFixture(
		t,
		root,
		"docs/handbook/auth.md",
		record(
			"auth",
			"Auth",
			"collection: handbook\nsidebar_position: 1\nrelated: [REQ-001]\n",
			"# Auth\n[Implementation](../../src/auth.go)\n[Directory](../../src/sessions/)",
		),
	)
	writeFixture(
		t,
		root,
		"docs/requirements/001-tokens.md",
		record("REQ-001", "Tokens", "collection: requirements\nstatus: draft\nrelated: [auth, ADR-001]\n", "# Tokens"),
	)
	writeFixture(
		t,
		root,
		"docs/decisions/001-tokens.md",
		record(
			"ADR-001",
			"Tokens",
			"collection: decisions\nstatus: proposed\ndate: 2026-09-01\nrelated: [REQ-001]\n",
			"# Tokens",
		),
	)
	result, err := s.Impact(t.Context(), []string{"src/auth.go", "src/sessions/deleted.go", "src/authentic.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 3 || !reflect.DeepEqual(result.UnmappedPaths, []string{"src/authentic.go"}) {
		t.Fatalf("impact: %+v", result)
	}
	if result.Documents[0].Reason != "direct_link" || len(result.Documents[0].Evidence) != 2 ||
		result.Documents[0].Evidence[0].Line < 1 {
		t.Fatalf("evidence: %+v", result.Documents[0])
	}
	if result.Documents[1].Reason != "related_document" || len(result.Documents[1].Evidence[0].Relationship) < 2 {
		t.Fatalf("relationship: %+v", result.Documents[1])
	}
	for _, paths := range [][]string{{"../outside"}, {"/outside"}, {"src/../../escape"}, nil} {
		if _, err = s.Impact(t.Context(), paths); err == nil {
			t.Fatalf("invalid paths %v accepted", paths)
		}
	}
}

func TestSourcePaginationResponseBoundsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	s := newDocs(t, root)
	writeFixture(
		t,
		root,
		"docs/handbook/large.md",
		record(
			"large",
			"Large",
			"collection: handbook\nsidebar_position: 1\n",
			strings.Repeat(strings.Repeat("<", 1000)+"\n", 240),
		),
	)
	result, err := s.GetDocument(t.Context(), "large", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > MaxResponseBytes || result.Source.NextStartLine == nil {
		t.Fatalf("size=%d source=%+v", len(data), result.Source)
	}
	next, err := s.GetDocument(t.Context(), "large", *result.Source.NextStartLine, 0)
	if err != nil || next.Source.StartLine != result.Source.EndLine+1 {
		t.Fatalf("continuation: %+v %v", next, err)
	}
	outside := t.TempDir()
	writeFixture(t, outside, "secret.md", record("secret", "Secret", "", "# Secret"))
	if err := os.Symlink(
		filepath.Join(outside, "secret.md"),
		filepath.Join(root, "docs/handbook/link.md"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetDocument(t.Context(), "secret", 0, 0); err == nil {
		t.Fatal("followed document symlink")
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := s.Catalog(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestMissingDocsAndMalformedMetadataDiagnostics(t *testing.T) {
	root := t.TempDir()
	s := newDocs(t, root)
	catalog, err := s.Catalog(t.Context())
	if err != nil || len(catalog.Diagnostics) != 1 {
		t.Fatalf("%+v %v", catalog, err)
	}
	writeFixture(t, root, "docs/handbook/bad.md", "---\nid: bad\ntitle: [wrong]\n---\n# Bad\n")
	writeFixture(t, root, "docs/README.md", "# Documentation\n")
	writeFixture(
		t,
		root,
		"docs/requirements/draft.md",
		record("REQ-001", "Draft", "collection: requirements\nstatus: draft\nrelated: [unknown]\n", "# Draft"),
	)
	result, err := s.GetDocument(t.Context(), "REQ-001", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.StatusMeaning, "Not accepted") || len(result.Diagnostics) != 1 {
		t.Fatalf("%+v", result)
	}
	catalog, err = s.Catalog(t.Context())
	if err != nil || len(catalog.Diagnostics) != 2 {
		t.Fatalf("%+v %v", catalog, err)
	}
}

func TestMetadataDiscoveryRespectsIgnoreRules(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "docs/README.md", "# Documentation\n")
	writeFixture(t, root, "README.md", "# Ordinary Markdown\n")
	writeFixture(t, root, "docs/handbook/plain.md", "# No metadata\n")
	writeFixture(
		t,
		root,
		".gitignore",
		"generated/\n*.private.md\n!published.private.md\ndocs/scratch/**\n!docs/scratch/kept.md\n",
	)
	writeFixture(t, root, "docs/notes/.gitignore", "draft-*\n!draft-published.md\n")
	for name, id := range map[string]string{
		"OVERVIEW.MD":              "overview",
		"notes/design.markdown":    "design",
		"published.private.md":     "published",
		"notes/draft-published.md": "draft-published",
		"scratch/kept.md":          "kept",
		"generated/copy.md":        "ignored-generated",
		"notes/secret.private.md":  "ignored-private",
		"notes/draft-hidden.md":    "ignored-nested",
		"scratch/hidden.md":        "ignored-descendant",
		".git/internal.md":         "ignored-git",
	} {
		writeFixture(t, root, "docs/"+name, record(id, id, "", "# Content"))
	}
	outside := t.TempDir()
	writeFixture(t, outside, "outside.md", record("outside", "Outside", "", "# Outside"))
	if err := os.Symlink(outside, filepath.Join(root, "docs/linked-directory")); err != nil {
		t.Fatal(err)
	}
	s := newDocs(t, root)
	catalog, err := s.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, doc := range catalog.Documents {
		if len(doc.ID) > 0 {
			ids = append(ids, doc.ID)
		}
	}
	slices.Sort(ids)
	if want := []string{
		"design",
		"draft-published",
		"kept",
		"overview",
		"published",
	}; !reflect.DeepEqual(ids, want) ||
		len(catalog.Diagnostics) != 0 {
		t.Fatalf("ids=%v diagnostics=%+v, want %v", ids, catalog.Diagnostics, want)
	}
	// Ignore changes are reflected without reconstructing the service.
	writeFixture(t, root, "docs/notes/.gitignore", "")
	if _, err := s.GetDocument(t.Context(), "ignored-nested", 0, 0); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionAndImpactFollowMetadata(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/auth.go", "package auth\n")
	writeFixture(t, root, "docs/policies/token.md", record("tokens", "Tokens",
		"collection: requirements\nstatus: accepted\nrelated: [storage]\n",
		"# Tokens\n[Code](../../src/auth.go)\n[Decision](../notes/storage.md)"))
	writeFixture(t, root, "docs/notes/storage.md", record("storage", "Storage",
		"collection: decisions\nstatus: accepted\ndate: 2026-09-01\n", "# Storage"))
	// The old conventional directory has no effect on collection or validation.
	writeFixture(t, root, "docs/requirements/generic.md", record("generic", "Generic", "", "# Generic"))
	s := newDocs(t, root)
	result, err := s.GetDocument(t.Context(), "tokens", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Document.Collection != "requirements" || len(result.Diagnostics) != 0 {
		t.Fatalf("document: %+v", result)
	}
	if len(result.Document.Links) != 2 || result.Document.Links[1].Kind != "document" {
		t.Fatalf("links: %+v", result.Document.Links)
	}
	generic, err := s.GetDocument(t.Context(), "generic", 0, 0)
	if err != nil || len(generic.Document.Collection) > 0 || len(generic.Diagnostics) != 0 {
		t.Fatalf("directory inferred metadata: %+v %v", generic, err)
	}
	impact, err := s.Impact(t.Context(), []string{"src/auth.go"})
	if err != nil || len(impact.Documents) != 2 || len(impact.UnmappedPaths) != 0 {
		t.Fatalf("impact: %+v %v", impact, err)
	}
	if impact.Documents[0].ID != "tokens" || impact.Documents[0].Reason != "direct_link" ||
		impact.Documents[1].ID != "storage" {
		t.Fatalf("evidence: %+v", impact.Documents)
	}
	if err := os.Rename(
		filepath.Join(root, "docs/policies/token.md"),
		filepath.Join(root, "docs/notes/renamed.md"),
	); err != nil {
		t.Fatal(err)
	}
	moved, err := s.GetDocument(t.Context(), "tokens", 0, 0)
	if err != nil || moved.Document.Path != "docs/notes/renamed.md" || moved.Document.Collection != "requirements" ||
		moved.Document.Hash != result.Document.Hash {
		t.Fatalf("moved document: %+v %v", moved, err)
	}
}
