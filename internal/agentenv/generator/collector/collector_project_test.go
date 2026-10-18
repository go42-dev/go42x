package collector

import (
	"reflect"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/config"
)

func TestProjectCollector(t *testing.T) {
	data, err := NewProjectCollector(nil).Collect(t.Context())
	if err != nil || len(data) != 0 {
		t.Fatalf("nil project = %v, %v", data, err)
	}
	cfg := &config.Config{
		Version: "1.0",
		Project: config.Project{
			Name:                    "example",
			Language:                "go",
			Description:             "description",
			Repository:              "github.com/example/repo",
			DocumentationEntrypoint: "team/index.md",
			Tags:                    []string{"cli"},
			Metadata:                map[string]string{"custom": "value"},
		},
		Providers: map[string]config.Provider{"claude": {}, "antigravity": {}},
		MCP: map[string]provider.MCPServer{
			"enabled": {Enabled: true, Command: "go42x"}, "disabled": {Command: "hidden"},
		},
	}
	data, err = NewProjectCollector(cfg).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"name": "example", "language": "go", "description": "description", "repository": "github.com/example/repo", "documentation-entrypoint": "team/index.md", "tags": []string{"cli"}, "metadata": map[string]string{"custom": "value"}} {
		if !reflect.DeepEqual(data[key], want) {
			t.Errorf("%s = %v, want %v", key, data[key], want)
		}
	}
	data, err = NewProjectCollector(&config.Config{}).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if data["documentation-entrypoint"] != config.DefaultDocumentationEntrypoint {
		t.Fatalf("default documentation entrypoint = %v", data["documentation-entrypoint"])
	}
	for _, key := range []string{"tags", "metadata", "mcp_servers"} {
		if _, ok := data[key]; ok {
			t.Errorf("empty optional %s should be omitted", key)
		}
	}
}
