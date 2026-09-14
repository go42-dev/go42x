package doctor_test

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/pkg/doctor"
	"github.com/go42-dev/go42x/pkg/doctor/check"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func TestServiceValidatesSettingsBeforeOptions(t *testing.T) {
	for _, settings := range []*doctor.Settings{nil, {}, {RootPath: "."}} {
		called := false
		service, err := doctor.NewService(settings, func(*doctor.Service) { called = true })
		if err == nil || !strings.Contains(err.Error(), "invalid settings:") || service != nil || called {
			t.Fatalf("invalid settings initialized service: service=%v err=%v options=%v", service, err, called)
		}
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = fmt.Sprintf("%s %d %s", info.Mode(), info.ModTime().UnixNano(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestServiceRegistersSubsystemsAndReloadsChecks(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(t.TempDir(), "index")
	knowledge := kwb.NewSettings()
	knowledge.RootPath, knowledge.IndexPath = root, indexPath
	kb, err := kwb.NewService(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kb.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := kb.Close(); err != nil {
		t.Fatal(err)
	}

	// All checks must inspect the selected checkout, even when invoked elsewhere.
	t.Chdir(t.TempDir())
	settings := doctor.NewSettings()
	settings.RootPath, settings.IndexPath = root, indexPath
	service, err := doctor.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	service.Register(check.Check{
		ID: "deployment.adapter", DependsOn: []string{"kwb.index"},
		Run: func(context.Context) check.Result {
			runs++
			return check.Result{Status: check.Pass, Message: "Test adapter is available"}
		},
	})
	beforeRoot, beforeIndex := snapshotTree(t, root), snapshotTree(t, indexPath)
	report, err := service.Run(t.Context())
	if err != nil || report.Status != check.Fail || report.SchemaVersion != 1 {
		t.Fatalf("report: %+v %v", report, err)
	}
	var ids []string
	var statuses []check.Status
	for _, result := range report.Checks {
		ids = append(ids, result.ID)
		statuses = append(statuses, result.Status)
	}
	if !reflect.DeepEqual(ids, []string{
		"agentenv.config", "agentenv.outputs", "agentenv.providers", "mcp.servers", "kwb.index", "deployment.adapter",
	}) || !reflect.DeepEqual(statuses, []check.Status{
		check.Fail, check.Skipped, check.Skipped, check.Skipped, check.Pass, check.Pass,
	}) || runs != 1 {
		t.Fatalf("registered checks: %+v; adapter runs: %d", report.Checks, runs)
	}
	if !reflect.DeepEqual(beforeRoot, snapshotTree(t, root)) ||
		!reflect.DeepEqual(beforeIndex, snapshotTree(t, indexPath)) {
		t.Fatal("doctor changed project or index files")
	}

	if err := os.MkdirAll(filepath.Join(root, ".go42x"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"go42x.yaml":    "version: '1.0'\nproject: {name: test}\ncontext: {template: agents.tpl.md}\nproviders: {codex: {enabled: false}}\n",
		"agents.tpl.md": "# Test\n",
	} {
		if err := os.WriteFile(filepath.Join(root, ".go42x", name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	beforeRoot, beforeIndex = snapshotTree(t, root), snapshotTree(t, indexPath)
	report, err = service.Run(t.Context())
	if err != nil || report.Status != check.Warn || report.Checks[0].Status != check.Pass ||
		report.Checks[1].Status != check.Warn || runs != 2 {
		t.Fatalf("updated configuration: %+v %v; adapter runs: %d", report, err, runs)
	}
	if !reflect.DeepEqual(beforeRoot, snapshotTree(t, root)) ||
		!reflect.DeepEqual(beforeIndex, snapshotTree(t, indexPath)) {
		t.Fatal("doctor changed project or index files on a subsequent run")
	}
}

func TestMCPChecksDependOnCurrentConfiguration(t *testing.T) {
	runtime := server.NewMCPServer("fixture", "1", server.WithToolCapabilities(false))
	runtime.AddTool(mcp.NewTool("known"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("doctor invoked a tool")
		return mcp.NewToolResultText("unexpected"), nil
	})
	handler := server.NewStreamableHTTPServer(runtime)
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()

	root := t.TempDir()
	configDir := filepath.Join(root, ".go42x")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "go42x.yaml")
	writeConfig := func(tool string) {
		t.Helper()
		content := fmt.Sprintf(
			"version: '1.0'\nproject: {name: test}\ncontext: {template: agents.tpl.md}\nproviders: {codex: {enabled: false}}\nmcp:\n  test:\n    enabled: true\n    name: test\n    type: http\n    url: %q\n    tools: [%q]\n",
			remote.URL,
			tool,
		)
		if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("known")
	if err := os.WriteFile(filepath.Join(configDir, "agents.tpl.md"), []byte("# Test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := doctor.NewSettings()
	settings.RootPath, settings.Timeout = root, 2*time.Second
	service, err := doctor.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	runMCP := func() check.Result {
		t.Helper()
		report, err := service.Run(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range report.Checks {
			if result.ID == "mcp.servers" {
				return result
			}
		}
		t.Fatal("MCP checks were not registered")
		return check.Result{}
	}
	result := runMCP()
	if result.Status != check.Pass || len(result.Children) != 2 || result.Children[1].Status != check.Skipped ||
		requests.Load() != 0 {
		t.Fatalf("probe ran without opt-in: %+v; requests=%d", result, requests.Load())
	}

	settings.ProbeMCP = true
	service, err = doctor.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	result = runMCP()
	if result.Status != check.Pass || result.Children[1].Status != check.Pass || requests.Load() == 0 {
		t.Fatalf("configured probe did not run: %+v; requests=%d", result, requests.Load())
	}
	writeConfig("missing")
	result = runMCP()
	if result.Status != check.Fail || !reflect.DeepEqual(result.Children[1].Evidence, []string{"missing"}) {
		t.Fatalf("probe used stale server definitions: %+v", result)
	}
	if err := os.WriteFile(configPath, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	result = runMCP()
	if result.Status != check.Skipped || !strings.Contains(result.Message, "agentenv.config") ||
		requests.Load() != before {
		t.Fatalf("probe ran with invalid configuration: %+v; requests=%d", result, requests.Load())
	}
}
