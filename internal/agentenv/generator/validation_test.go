package generator

import (
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/config"
)

func TestRenderedHeadingValidation(t *testing.T) {
	const main = "# Any title\n\n## Any section\n\n{{ .chunks }}\n"

	for _, tt := range []struct {
		name   string
		main   string
		chunks []string
		want   string
	}{
		{
			name:   "arbitrary section names and siblings",
			main:   main,
			chunks: []string{"### Alpha\n\n#### Details", "### Beta\n\n### Gamma"},
		},
		{
			name:   "chunk directly below title",
			main:   "# Custom\n\n{{ .chunks }}",
			chunks: []string{"## Rules\n\n### Details"},
		},
		{
			name:   "setext headings",
			main:   "Custom\n======\n\nRules\n-----\n\n{{ .chunks }}",
			chunks: []string{"### Details"},
		},
		{
			name:   "code blocks and HTML are ignored",
			main:   main,
			chunks: []string{"### Rules\n\n```md\n# Example\n###### Example\n```\n\n~~~\n# Example\n~~~\n\n    # Example\n\n<!--\n# Example\n-->"},
		},
		{
			name:   "headings without text",
			main:   "#\n\n##\n\n{{ .chunks }}",
			chunks: []string{"###"},
		},
		{
			name:   "chunks without headings",
			main:   main,
			chunks: []string{"Plain text.", "", "### Rules"},
		},
		{
			name:   "unused chunks",
			main:   "# Custom\n",
			chunks: []string{"# Invalid if included"},
		},
		{
			name:   "conditional headings are evaluated",
			main:   main,
			chunks: []string{"{{if .enabled}}# Hidden{{else}}### Visible{{end}}"},
		},
		{
			name:   "dynamic headings",
			main:   main,
			chunks: []string{"{{.heading}}"},
		},
		{
			name:   "control flow crosses chunk boundary",
			main:   main,
			chunks: []string{"{{if not .enabled}}\n### First", "### Second\n{{end}}"},
		},
		{
			name:   "tables retain Markdown semantics",
			main:   main,
			chunks: []string{"### Rules\n\n| Item | Meaning |\n| --- | --- |\n| # Value | ###### Value |"},
		},
		{
			name:   "main section resumes after chunks",
			main:   main + "\n## Later\n\n### Details\n",
			chunks: []string{"### Rules\n\n#### Details"},
		},
		{
			name: "missing title",
			main: "Plain text.",
			want: "one H1 document title",
		},
		{
			name: "second title",
			main: "# First\n\n# Second\n",
			want: "main.tpl.md:3: only one H1",
		},
		{
			name: "initial skipped level",
			main: "## Missing title\n",
			want: "main.tpl.md:1: heading skips from H0 to H2",
		},
		{
			name: "main template skips level",
			main: "# Custom\n\n### Skipped\n",
			want: "main.tpl.md:3: heading skips from H1 to H3",
		},
		{
			name:   "first chunk heading too deep",
			main:   main,
			chunks: []string{"\n\n#### Nested\n"},
			want:   "chunks/00.tpl.md:3: first chunk heading is H4; expected H3",
		},
		{
			name:   "second chunk belongs to insertion section",
			main:   main,
			chunks: []string{"### First\n\n#### Details", "#### Incorrectly nested"},
			want:   "chunks/01.tpl.md:1: first chunk heading is H4; expected H3",
		},
		{
			name:   "chunk escapes containing section",
			main:   main,
			chunks: []string{"### First\n\n## Escaped"},
			want:   "chunks/00.tpl.md:3: chunk heading H2 escapes",
		},
		{
			name:   "chunk skips internal level",
			main:   main,
			chunks: []string{"### First\n\n##### Skipped"},
			want:   "chunks/00.tpl.md:3: heading skips from H3 to H5",
		},
		{
			name:   "chunk cannot provide title",
			main:   "{{ .chunks }}",
			chunks: []string{"# Title"},
			want:   "chunks/00.tpl.md:1: chunk headings need a containing section",
		},
		{
			name:   "rendered skip after conditional",
			main:   main,
			chunks: []string{"### First\n{{if .enabled}}#### Hidden{{end}}\n##### Skipped"},
			want:   "chunks/00.tpl.md:3: heading skips from H3 to H5",
		},
		{
			name:   "dynamic invalid heading",
			main:   main,
			chunks: []string{"### First\n\n{{printf \"##### %s\" .heading}}"},
			want:   "chunks/00.tpl.md:3: heading skips from H3 to H5",
		},
		{
			name:   "quoted headings follow Markdown semantics",
			main:   main,
			chunks: []string{"> ### First\n>\n> ##### Skipped"},
			want:   "chunks/00.tpl.md:3: heading skips from H3 to H5",
		},
		{
			name: "setext duplicate title",
			main: "First\n=====\n\nSecond\n======\n",
			want: "main.tpl.md:4: only one H1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeInstructionTemplates(t, dir, tt.main, tt.chunks)

			rendered, err := newTemplateEngine(dir).Render(config.Context{
				Template:  "main.tpl.md",
				ChunksDir: "chunks",
			}, map[string]any{"heading": "### Dynamic"})

			if err != nil {
				t.Fatal(err)
			}

			err = validateInstructions(rendered)

			if len(tt.want) == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateInstructions() error = %v, want %q", err, tt.want)
			}
		})
	}
}
