package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/config"
)

func TestTemplateSourcesPreserveExecution(t *testing.T) {
	const main = `{{define "section"}}### {{.}}{{end -}}
# {{.title}}

## Rules

{{$heading := "Shared" -}}
{{range .items}}{{ .chunks }}{{end}}
`
	chunks := []string{
		"{{template \"section\" $heading}}\n\n{{with .}}#### {{.}}{{end}}\n",
		"{{- if .}}\n\n### More {{.}}\n{{else}}### Empty{{end}}\n\n",
	}
	dir := t.TempDir()
	writeInstructionTemplates(t, dir, main, chunks)
	e := newTemplateEngine(dir)
	data := map[string]any{
		"title": "Custom",
		"items": []string{"First", "Second"},
	}
	raw := strings.Replace(main, chunksPlaceholder,
		strings.TrimSpace(chunks[0])+"\n\n"+strings.TrimSpace(chunks[1]), 1)
	want, err := e.Process(raw, data)
	if err != nil {
		t.Fatal(err)
	}

	got, err := e.Render(config.Context{
		Template:  "main.tpl.md",
		ChunksDir: "chunks",
	}, data)

	if err != nil {
		t.Fatal(err)
	}
	if got.content != want {
		t.Fatalf("Render() = %q; want %q", got.content, want)
	}
}

func TestTemplateSourcesConcurrentRendering(t *testing.T) {
	dir := t.TempDir()
	writeInstructionTemplates(t, dir, "# {{.title}}\n\n## Rules\n\n{{ .chunks }}", []string{
		"### {{.title}}\n\n#### Details",
	})
	engine := newTemplateEngine(dir)
	for _, title := range []string{"First", "Second", "Third"} {
		t.Run(title, func(t *testing.T) {
			t.Parallel()
			content, err := engine.Render(config.Context{
				Template:  "main.tpl.md",
				ChunksDir: "chunks",
			}, map[string]any{"title": title})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(content.content, "### "+title+"\n") {
				t.Fatalf("Render() = %q", content.content)
			}
		})
	}
}

func writeInstructionTemplates(t *testing.T, dir, main string, chunks []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.tpl.md"), []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "chunks"), 0755); err != nil {
		t.Fatal(err)
	}
	for i, content := range chunks {
		path := filepath.Join(dir, "chunks", fmt.Sprintf("%02d.tpl.md", i))
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTemplateRejectsMissingKeys(t *testing.T) {
	t.Parallel()
	data := map[string]any{"project": map[string]any{"name": "example"}, "mcp": map[string]any{}}
	for _, tt := range []struct {
		name, chunk, want string
	}{
		{"typo", "### Project\n\n{{ .project.nme }}\n", "chunks/00.tpl.md:3: prints a key"},
		{"missing section", "### Project\n\nLine\n{{ .projct.name }}\n", "chunks/00.tpl.md:4: prints a key"},
		{"optional key", "### Project\n\n{{ if .mcp.gopls }}gopls{{ end }}{{ .project.name }}\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeInstructionTemplates(t, dir, "# Title\n\n## Rules\n\n{{ .chunks }}", []string{tt.chunk})
			engine := newTemplateEngine(dir)

			rendered, err := engine.Render(config.Context{Template: "main.tpl.md", ChunksDir: "chunks"}, data)

			if len(tt.want) == 0 {
				if err != nil || !strings.Contains(rendered.content, "example") {
					t.Fatalf("Render() = %v, %v", rendered, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Render() error = %v, want %q", err, tt.want)
			}
		})
	}

	if _, err := newTemplateEngine("").Process("name: {{ .project.nme }}\n", data); err == nil ||
		!strings.Contains(err.Error(), "output line 1 prints a key") {
		t.Errorf("Process() error = %v, want the missing key", err)
	}
}
