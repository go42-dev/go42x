package kwb

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func TestRunnersUseTypedSettingsAndFactoryStreams(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "auth.md"),
		[]byte("# Authentication\nrefresh token\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	knowledge := kwb.NewSettings()
	knowledge.RootPath = root
	knowledge.IndexPath = filepath.Join(root, ".go42x/kwb/index")
	service, err := kwb.NewService(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	// Runners must use the supplied settings even when CLI globals disagree.
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("root", t.TempDir())
	viper.Set("index", "missing")
	viper.Set("json", false)
	viper.Set("limit", -1)
	factory := cmdutil.NewFactory(t.Context())
	var output, errors bytes.Buffer
	factory.SetIOStreams(bytes.NewReader(nil), &output, &errors)
	cases := []struct {
		name string
		run  func() error
	}{
		{"search", func() error {
			return runSearchCommand(
				factory,
				&kwb.SearchSettings{
					KnowledgeBase: knowledge,
					Search:        kwb.SearchOptions{Query: "refresh token", Limit: 10},
					JSON:          true,
				},
			)
		}},
		{"read", func() error {
			return runReadCommand(
				factory,
				&kwb.ReadSettings{KnowledgeBase: knowledge, Path: "auth.md", StartLine: 2, EndLine: 2, JSON: true},
			)
		}},
		{
			"stats",
			func() error {
				return runStatsCommand(factory, &kwb.StatsSettings{KnowledgeBase: knowledge, JSON: true})
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output.Reset()
			errors.Reset()
			if err := test.run(); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(&output)
			var result map[string]any
			if err := decoder.Decode(&result); err != nil {
				t.Fatal(err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("unexpected extra output: %v", err)
			}
			if errors.Len() != 0 {
				t.Fatalf("stderr: %s", errors.String())
			}
			switch test.name {
			case "search":
				if result["total"] != float64(1) {
					t.Fatalf("search: %+v", result)
				}
			case "read":
				if result["content"] != "refresh token\n" {
					t.Fatalf("read: %+v", result)
				}
			case "stats":
				if result["document_count"] != float64(1) {
					t.Fatalf("stats: %+v", result)
				}
			}
		})
	}
}
