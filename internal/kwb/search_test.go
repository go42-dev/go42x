package kwb

import (
	"path/filepath"
	"slices"
	"testing"
)

func relaxedFixture(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	service := testService(t, root, filepath.Join(t.TempDir(), "index"), nil)
	writeFixture(t, root, "freshness.go",
		"package kwb\n\n// CheckFreshness detects a stale index after files change.\nfunc CheckFreshness() {}\n")
	writeFixture(t, root, "guide.md", "# Index maintenance\nRebuild the knowledge base when it is stale.\n")
	writeFixture(t, root, "other.go", "package kwb\n\nfunc Unrelated() {}\n")
	buildFixture(t, service, root)
	return service
}

func TestSearchRelaxesQuestionsWithoutFullMatch(t *testing.T) {
	t.Parallel()
	service := relaxedFixture(t)
	tests := []struct {
		name    string
		options SearchOptions
		relaxed bool
		paths   []string
	}{
		{
			name:    "question",
			options: SearchOptions{Query: "detect stale index after editing"},
			relaxed: true,
			paths:   []string{"freshness.go", "guide.md"},
		},
		{
			name:    "question with filter",
			options: SearchOptions{Query: "detect stale index after editing", Kind: KindCode},
			relaxed: true,
			paths:   []string{"freshness.go"},
		},
		{
			name:    "strict match",
			options: SearchOptions{Query: "CheckFreshness"},
			paths:   []string{"freshness.go"},
		},
		{
			name:    "one missing word",
			options: SearchOptions{Query: "nonexistentword"},
		},
		{
			name:    "unrelated words",
			options: SearchOptions{Query: "quasar frobnicator"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := service.Search(t.Context(), tt.options)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, hit := range result.Results {
				paths = append(paths, hit.Path)
			}
			slices.Sort(paths)
			if result.Relaxed != tt.relaxed || !slices.Equal(paths, tt.paths) || int(result.Total) != len(tt.paths) {
				t.Errorf("relaxed=%v paths=%v total=%d, want relaxed=%v paths=%v",
					result.Relaxed, paths, result.Total, tt.relaxed, tt.paths)
			}
		})
	}
}

func TestSearchRelaxedPagesAreStable(t *testing.T) {
	t.Parallel()
	service := relaxedFixture(t)
	var paths []string
	for offset := range 2 {
		result, err := service.Search(t.Context(), SearchOptions{
			Query:  "detect stale index after editing",
			Limit:  1,
			Offset: offset,
		})
		if err != nil || !result.Relaxed || result.Total != 2 || len(result.Results) != 1 {
			t.Fatalf("page %d: %+v %v", offset, result, err)
		}
		if (offset == 0) != (result.NextOffset != nil) {
			t.Fatalf("page %d continuation: %+v", offset, result.NextOffset)
		}
		paths = append(paths, result.Results[0].Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"freshness.go", "guide.md"}) {
		t.Fatalf("pages repeat or skip results: %v", paths)
	}
}
