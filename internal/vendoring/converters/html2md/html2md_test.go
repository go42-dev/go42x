package html2md

import (
	"strings"
	"testing"
)

// TestConvertTablesAndStrikethrough also covers a cell with two paragraphs, which the
// table plugin would otherwise skip, leaving the table flattened.
func TestConvertTablesAndStrikethrough(t *testing.T) {
	t.Parallel()
	const document = `<!DOCTYPE html>
<html>
<body>
<h1>Sources</h1>
<table><tbody>
<tr><th><p>Source</p></th><th><p>Auth</p></th></tr>
<tr><td><p>http</p></td><td><p>token-env</p><p>or user-env</p></td></tr>
</tbody></table>
<p><del>Removed</del> and <a href="/doc/auth">kept</a>.</p>
</body>
</html>`
	markdown, err := NewHTMLToMarkdownConverter().Convert([]byte(document))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"# Sources",
		"| Source | Auth |\n|---|---|",
		"| http | token-env<br />",
		"or user-env |",
		"~~Removed~~",
		"[kept](/doc/auth)",
	} {
		if !strings.Contains(string(markdown), want) {
			t.Errorf("Markdown omitted %q:\n%s", want, markdown)
		}
	}
	if !strings.HasSuffix(string(markdown), ".\n") || strings.HasSuffix(string(markdown), "\n\n") {
		t.Errorf("Markdown must end with exactly one newline: %q", markdown)
	}
}
