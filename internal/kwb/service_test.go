package kwb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contextFixture(t *testing.T, configure ...func(*Settings)) (string, *Service) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"docs/README.md":                 "---\nid: docs-home\ntitle: Documentation\ncollection: overview\n---\n# Documentation\n",
		"docs/handbook/project.md":       "---\nid: project\ntitle: Project\ncollection: handbook\nsidebar_position: 1\n---\n# Project\n",
		"docs/handbook/conventions.md":   "---\nid: conventions\ntitle: Conventions\ncollection: handbook\nsidebar_position: 2\n---\n# Conventions\n",
		"docs/handbook/documentation.md": "---\nid: documentation\ntitle: Documentation policy\ncollection: handbook\nsidebar_position: 3\n---\n# Documentation policy\n",
		"docs/handbook/auth.md":          "---\nid: auth\ntitle: Refresh tokens\ncollection: handbook\nsidebar_position: 4\nrelated: [REQ-001]\n---\n# Refresh tokens\n[Code](../../src/auth.go)\n",
		"docs/requirements/001-auth.md":  "---\nid: REQ-001\ntitle: Token rotation\ncollection: requirements\nstatus: draft\nrelated: [ADR-001]\n---\n# Token rotation\nDraft rotation requirement.\n",
		"docs/decisions/001-auth.md":     "---\nid: ADR-001\ntitle: Token rotation\ncollection: decisions\nstatus: proposed\ndate: 2026-09-01\n---\n# Token rotation\n",
		"docs/templates/requirement.md":  "---\nid: template-requirement\ntitle: Requirement template\ncollection: templates\n---\n# New requirement template\n",
		"src/auth.go":                    "package auth\n// RotateRefreshToken rotates tokens.\nfunc RotateRefreshToken() {}\n",
	}
	for path, content := range files {
		writeFixture(t, root, path, content)
	}
	service := testService(t, root, filepath.Join(root, ".go42x/kwb/index"), func(settings *Settings) {
		for _, apply := range configure {
			apply(settings)
		}
	})
	return service.settings.RootPath, service
}

func hasDiagnostic(result *ContextResult, code string) bool {
	if result == nil {
		return false
	}

	for _, d := range result.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestServiceValidatesSettingsBeforeOptions(t *testing.T) {
	oversized := NewSettings()
	oversized.DefaultContentBytes = MaxContentBytes + 1
	for _, settings := range []*Settings{nil, {}, oversized} {
		called := false
		service, err := NewService(settings, func(*Service) {
			called = true
		})
		if err == nil || !strings.Contains(err.Error(), "invalid settings:") || service != nil || called {
			t.Fatalf("invalid settings initialized service: service=%v err=%v options=%v", service, err, called)
		}
	}
}

func TestUnifiedServiceFollowsIndexedRoot(t *testing.T) {
	root, service := contextFixture(t)
	if _, err := service.GetDocument(t.Context(), "docs-home", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	otherRoot, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, otherRoot, "docs/README.md", "---\nid: other-home\ntitle: Other project\n---\n# Other project\n")
	if _, err := service.BuildIndex(t.Context(), otherRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetDocument(t.Context(), "docs-home", 0, 0); err == nil {
		t.Fatal("document from previous root remained in the catalog")
	}

	reader := testService(t, t.TempDir(), service.settings.IndexPath, nil)
	for _, current := range []*Service{service, reader} {
		document, err := current.GetDocument(t.Context(), "other-home", 0, 0)
		if err != nil ||
			document.Source.Content != "---\nid: other-home\ntitle: Other project\n---\n# Other project\n" {
			t.Fatalf("indexed root document: %+v %v", document, err)
		}
		result, err := current.Context(t.Context(), ContextOptions{})
		if err != nil || result.Root != otherRoot || len(result.Items) != 1 ||
			result.Items[0].Document.ID != "other-home" {
			t.Fatalf("indexed root context: %+v %v", result, err)
		}
	}
}

func TestLiveDocumentationSurvivesUnavailableIndex(t *testing.T) {
	for _, state := range []string{"missing", "corrupt", "incompatible"} {
		t.Run(state, func(t *testing.T) {
			root, service := contextFixture(t, func(settings *Settings) {
				settings.RequireRootMatch = true
			})

			if state != "missing" {
				if _, err := service.BuildIndex(t.Context(), root); err != nil {
					t.Fatal(err)
				}
				if state == "corrupt" {
					writeFixture(t, service.settings.IndexPath, "CURRENT", "invalid-generation")
				} else {
					generation, err := service.index.(*indexManager).generation()
					if err != nil {
						t.Fatal(err)
					}
					directory := filepath.Join(service.settings.IndexPath, generation)
					meta, err := readManifest(directory)
					if err != nil {
						t.Fatal(err)
					}
					meta.Version++
					data, err := json.Marshal(meta)
					if err != nil {
						t.Fatal(err)
					}
					writeFixture(t, directory, "manifest.json", string(data))
				}
			}
			if _, err := service.GetDocument(t.Context(), "auth", 0, 0); err != nil {
				t.Fatal(err)
			}
			impact, err := service.Impact(t.Context(), []string{"src/auth.go"})
			if err != nil || len(impact.Documents) == 0 {
				t.Fatalf("impact: %+v %v", impact, err)
			}
			result, err := service.Context(t.Context(), ContextOptions{})
			if err != nil || hasDiagnostic(result, "index_unavailable") || len(result.Items) == 0 {
				t.Fatalf("context: %+v %v", result, err)
			}
			if state == "missing" {
				if _, err := os.Stat(service.settings.IndexPath); !os.IsNotExist(err) {
					t.Fatalf("read created missing index: %v", err)
				}
			}
		})
	}
}

// NewServiceForTest supplies internal accessors to external tests without changing the production API.
func NewServiceForTest(t *testing.T, settings *Settings, index indexAccessor, catalog catalogAccessor) *Service {
	t.Helper()
	service, err := NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	service.index = index
	service.catalog = catalog
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	return service
}
