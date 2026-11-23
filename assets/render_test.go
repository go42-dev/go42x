package assets

import (
	"strings"
	"testing"
)

func TestCheckMissingValue(t *testing.T) {
	t.Parallel()
	for output, want := range map[string]string{
		"":                            "",
		"name: example\n":             "",
		"name: <no value>\n":          "output line 1 prints a key",
		"a: 1\nb: 2\nc: <no value>\n": "output line 3 prints a key",
	} {
		err := CheckMissingValue(output)
		if len(want) == 0 && err != nil || len(want) > 0 && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("CheckMissingValue(%q) = %v, want %q", output, err, want)
		}
	}
}

func TestRenderRejectsMissingKeys(t *testing.T) {
	t.Parallel()
	if _, err := Render(VendorManifest, map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "output line 3 prints a key") {
		t.Errorf("Render() error = %v, want the missing .vendoring key", err)
	}

	// The MCP import tests the optional server list with if.
	content, err := Render(ProviderTemplate("gh-aw", "900-aw-mcp.tpl.md"), map[string]any{})
	if err != nil || !strings.Contains(content, "mcp-servers: {}\n") {
		t.Errorf("Render() = %q, %v", content, err)
	}
}
