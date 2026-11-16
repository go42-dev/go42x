package kwb

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGoChunksSkipBoilerplate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name: "declarations and comments",
			source: `// Package example shows chunk boundaries.
package example

import (
	"fmt"
	"strings"
)

//go:embed testdata

// Helpers below format values.

// Format prints a value.
func Format(v any) string { return fmt.Sprint(v) }

type Printer struct{}

func (Printer) Print(v string) string { return strings.TrimSpace(v) }
`,
			want: []string{
				"1-3 package example []",
				"8-12 package example []",
				"13-14 func Format [Format]",
				"16-16 type Printer [Printer]",
				"18-18 func Printer.Print [Printer.Print Print]",
			},
		},
		{
			name:   "directives only",
			source: "package example\n\n//go:generate echo\n//nolint:all\n\nfunc Run() {}\n",
			want:   []string{"6-6 func Run [Run]"},
		},
		{
			name:   "package clause only",
			source: "package example\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, doc := range chunkFile("example.go", []byte(tt.source)) {
				got = append(got, fmt.Sprintf("%d-%d %s %v", doc.StartLine, doc.EndLine, doc.Title, doc.Symbols))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("chunks = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchSkipsGoBoilerplate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := testService(t, root, filepath.Join(t.TempDir(), "index"), nil)
	writeFixture(t, root, "ledger/ledger.go",
		"package ledger\n\nimport \"fmt\"\n\n// Post records an entry.\nfunc Post() { fmt.Println() }\n")
	buildFixture(t, service, root)
	result, err := service.Search(t.Context(), SearchOptions{Query: "ledger", Kind: KindCode})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].Title != "func Post" {
		t.Fatalf("boilerplate returned: %+v", result.Results)
	}
}

func TestDocumentationIndexesIdentifierWords(t *testing.T) {
	t.Parallel()
	docs := chunkFile("guide.md", []byte("# Tools\nUse go_symbol_references and kwbSearch before editing.\n"))
	if len(docs) != 1 {
		t.Fatalf("chunks = %+v", docs)
	}
	words := strings.Fields(docs[0].Code)
	for _, want := range []string{"symbol", "references", "kwb", "Search"} {
		if !slices.Contains(words, want) {
			t.Errorf("identifier word %q missing from %q", want, docs[0].Code)
		}
	}
	for _, unwanted := range []string{"Use", "before", "editing"} {
		if slices.Contains(words, unwanted) {
			t.Errorf("prose word %q duplicated into %q", unwanted, docs[0].Code)
		}
	}

	root := t.TempDir()
	service := testService(t, root, filepath.Join(t.TempDir(), "index"), nil)
	writeFixture(t, root, "guide.md", "# Tools\nUse go_symbol_references before editing.\n")
	buildFixture(t, service, root)
	result, err := service.Search(t.Context(), SearchOptions{Query: "symbol references", Kind: KindDocumentation})
	if err != nil || len(result.Results) != 1 || result.Results[0].Path != "guide.md" {
		t.Fatalf("identifier words not searchable: %+v %v", result, err)
	}
}
