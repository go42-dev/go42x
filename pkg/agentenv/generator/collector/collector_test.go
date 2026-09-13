package collector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

func TestCollectorMetadata(t *testing.T) {
	collectors := []struct {
		collector interface {
			Name() string
			Priority() int
		}
		name     string
		priority int
	}{
		{NewProjectCollector(nil), "project", 5}, {NewGitCollector("."), "git", 10},
		{NewEnvironmentCollector(nil, "."), "environment", 20}, {NewGitHubActionsCollector(), "github_actions", 30},
	}
	for _, tt := range collectors {
		if tt.collector.Name() != tt.name || tt.collector.Priority() != tt.priority {
			t.Errorf("collector metadata for %s", tt.name)
		}
	}
	data, err := NewBaseCollector("base", 1).Collect(t.Context())
	if err != nil || data != nil {
		t.Fatalf("base Collect() = %v, %v", data, err)
	}
}

func TestProjectCollector(t *testing.T) {
	data, err := NewProjectCollector(nil).Collect(t.Context())
	if err != nil || len(data) != 0 {
		t.Fatalf("nil project = %v, %v", data, err)
	}
	cfg := &config.Config{
		Version: "1.0",
		Project: config.Project{
			Name:        "example",
			Language:    "go",
			Description: "description",
			Tags:        []string{"cli"},
			Metadata:    map[string]string{"repository": "example/repo"},
		},
		Providers: map[string]config.Provider{"claude": {}, "gemini": {}},
		MCP: map[string]config.MCPServer{
			"enabled": {Enabled: true, Command: "go42x"}, "disabled": {Command: "hidden"},
		},
	}
	data, err = NewProjectCollector(cfg).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"name": "example", "language": "go", "description": "description", "tags": []string{"cli"}, "metadata": map[string]string{"repository": "example/repo"}} {
		if !reflect.DeepEqual(data[key], want) {
			t.Errorf("%s = %v, want %v", key, data[key], want)
		}
	}
	data, err = NewProjectCollector(&config.Config{}).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tags", "metadata", "mcp_servers"} {
		if _, ok := data[key]; ok {
			t.Errorf("empty optional %s should be omitted", key)
		}
	}
}

func TestEnvironmentCollector(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CI", "true")
	t.Setenv("AGENTENV_TEST_INCLUDED", "yes")
	t.Setenv("AGENTENV_TEST_EMPTY", "")
	t.Setenv("AGENTENV_TEST_PRIVATE", "secret")
	data, err := NewEnvironmentCollector(
		[]string{"AGENTENV_TEST_INCLUDED", "AGENTENV_TEST_EMPTY"},
		".",
	).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"is_ci":       true,
		"ci_mode":     "true",
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"working_dir": wd,
	} {
		if data[key] != want {
			t.Errorf("%s = %v, want %v", key, data[key], want)
		}
	}
	variables := data["variables"].(map[string]string)
	if variables["AGENTENV_TEST_INCLUDED"] != "yes" {
		t.Error("configured environment variable missing")
	}
	for _, key := range []string{"AGENTENV_TEST_PRIVATE", "AGENTENV_TEST_EMPTY"} {
		if _, ok := variables[key]; ok {
			t.Errorf("unrequested or empty env var %s exposed", key)
		}
	}
	t.Setenv("CI", "")
	data, err = NewEnvironmentCollector(nil, ".").Collect(t.Context())
	if err != nil || data["variables"] != nil || data["is_ci"] != false || data["ci_mode"] != "" {
		t.Fatalf("unconfigured environment variables = %v, %v", data, err)
	}
}

func cleanActionsEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "RUNNER_") {
			t.Setenv(key, "")
		}
	}
	for _, key := range []string{"REPOSITORY", "EVENT_NAME", "IS_PR", "ISSUE_NUMBER", "PR_TITLE", "PR_BODY", "PR_BASE", "PR_HEAD", "USER_REQUEST", "ACTOR"} {
		t.Setenv(key, "")
	}
}

func TestGitHubActionsContext(t *testing.T) {
	cleanActionsEnv(t)
	c := NewGitHubActionsCollector()
	data, err := c.Collect(t.Context())
	if err != nil || len(data) != 0 {
		t.Fatalf("outside Actions = %v, %v", data, err)
	}
	for key, value := range map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "https://github.example", "GITHUB_REPOSITORY": "org/repo", "GITHUB_REPOSITORY_OWNER": "org", "GITHUB_RUN_ID": "42", "GITHUB_EVENT_NAME": "pull_request", "GITHUB_ACTOR": "actor", "GITHUB_TRIGGERING_ACTOR": "trigger", "GITHUB_WORKFLOW": "test", "GITHUB_WORKFLOW_REF": "ref", "GITHUB_WORKFLOW_SHA": "abc", "GITHUB_JOB": "unit", "GITHUB_SHA": "abc", "GITHUB_REF": "refs/heads/main", "GITHUB_RUN_NUMBER": "5", "GITHUB_RUN_ATTEMPT": "2",
		"RUNNER_NAME": "runner", "RUNNER_OS": "Linux", "RUNNER_ARCH": "X64", "RUNNER_TEMP": "/tmp/runner", "RUNNER_TOOL_CACHE": "/tmp/tools",
		"GITHUB_EVENT_PAYLOAD": `{"pull_request":{"number":7,"title":"Fix tests","body":"Body","head":{"ref":"feature"},"base":{"ref":"main"}},"review":{"body":"Review this"}}`,
	} {
		t.Setenv(key, value)
	}
	data, err = c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"repository":   map[string]any{"full_name": "org/repo", "owner": "org"},
		"actor":        map[string]any{"login": "actor", "triggering_actor": "trigger"},
		"workflow":     map[string]any{"name": "test", "ref": "ref", "sha": "abc"},
		"runner":       map[string]any{"name": "runner", "os": "Linux", "arch": "X64", "temp_dir": "/tmp/runner", "tool_cache": "/tmp/tools"},
		"pull_request": map[string]any{"number": 7, "is_pr": true, "title": "Fix tests", "body": "Body", "base": "main", "head": "feature"},
		"build_url":    "https://github.example/org/repo/actions/runs/42", "user_request": "Review this", "job": "unit", "sha": "abc", "run_attempt": "2",
	} {
		if !reflect.DeepEqual(data[key], want) {
			t.Errorf("%s = %#v, want %#v", key, data[key], want)
		}
	}
	for key, value := range map[string]string{
		"REPOSITORY": "override/repo", "EVENT_NAME": "issue_comment", "ACTOR": "override",
		"USER_REQUEST": "override request", "ISSUE_NUMBER": "99", "IS_PR": "false",
		"PR_TITLE": "override title", "PR_BODY": "override body", "PR_HEAD": "override head", "PR_BASE": "override base",
	} {
		t.Setenv(key, value)
	}
	after, err := c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, data) {
		t.Fatalf("legacy environment variables changed GitHub context: %v", after)
	}
	t.Setenv("GITHUB_EVENT_PAYLOAD", "{}")
	data, err = c.Collect(t.Context())
	if err != nil || data["issue"] != nil || data["pull_request"] != nil || data["user_request"] != nil {
		t.Fatalf("legacy environment variables supplied event context: %v, %v", data, err)
	}
}

func TestGitHubActionsPrepareEnv(t *testing.T) {
	for _, tt := range []struct {
		name, event, action, payload, headRef, refName, subject, request string
		details                                                          map[string]any
	}{
		{
			name: "issue comment", event: "issue_comment", action: "created", refName: "main",
			payload: `{"action":"created","issue":{"number":7,"title":"Fix the issue","body":null,"html_url":"https://github.example/org/repo/issues/7"},"comment":{"body":"@agent fix this"}}`,
			subject: "issue", request: "@agent fix this",
			details: map[string]any{"number": 7, "is_pr": false, "title": "Fix the issue", "url": "https://github.example/org/repo/issues/7"},
		},
		{
			name: "PR conversation comment", event: "issue_comment", action: "created", refName: "main",
			payload: `{"action":"created","issue":{"number":8,"title":"Fix the PR","body":"PR description","pull_request":{"html_url":"https://github.example/org/repo/pull/8"}},"comment":{"body":"@agent update this PR"}}`,
			subject: "pull_request", request: "@agent update this PR",
			details: map[string]any{"number": 8, "is_pr": true, "title": "Fix the PR", "body": "PR description", "url": "https://github.example/org/repo/pull/8"},
		},
		{
			name: "PR review comment", event: "pull_request_review_comment", action: "created",
			headRef: "environment-head", refName: "9/merge",
			payload: `{"action":"created","pull_request":{"number":9,"title":"Review the change","head":{"ref":"payload-head"},"base":{"ref":"main"}},"comment":{"body":"@agent fix this line"}}`,
			subject: "pull_request", request: "@agent fix this line",
			details: map[string]any{"number": 9, "is_pr": true, "title": "Review the change", "head": "payload-head", "base": "main"},
		},
		{
			name: "PR review with head fallback", event: "pull_request_review", action: "submitted",
			headRef: "feature", refName: "10/merge",
			payload: `{"action":"submitted","number":10,"pull_request":{"title":"Review the PR","base":{"ref":"main"}},"review":{"body":"@agent address this review"}}`,
			subject: "pull_request", request: "@agent address this review",
			details: map[string]any{"number": 10, "is_pr": true, "title": "Review the PR", "head": "feature", "base": "main"},
		},
		{
			name: "manual workflow", event: "workflow_dispatch", refName: "main",
			payload: `{"repository":{"full_name":"payload/repository"}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleanActionsEnv(t)
			// Match the variables exported by go42's ai-prepare-env/action.yml.
			for key, value := range map[string]string{
				"CI": "true", "GITHUB_ACTIONS": "true", "GITHUB_ACTOR": "actor",
				"GITHUB_REPOSITORY": "org/repo", "GITHUB_EVENT_NAME": tt.event,
				"GITHUB_EVENT_PAYLOAD": tt.payload, "GITHUB_HEAD_REF": tt.headRef,
				"GITHUB_REF_NAME": tt.refName, "GITHUB_SHA": "checkout-sha",
				"GITHUB_SERVER_URL": "https://github.example/", "GITHUB_RUN_ID": "42",
			} {
				t.Setenv(key, value)
			}
			data, err := NewGitHubActionsCollector().Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			event := map[string]any{"name": tt.event}
			if tt.action != "" {
				event["action"] = tt.action
			}
			for key, want := range map[string]any{
				"repository": map[string]any{"full_name": "org/repo", "owner": "org"},
				"actor":      map[string]any{"login": "actor"},
				"event":      event, "event_name": tt.event, "ref_name": tt.refName,
				"sha": "checkout-sha", "run_id": "42", "server_url": "https://github.example/",
				"build_url": "https://github.example/org/repo/actions/runs/42",
			} {
				if !reflect.DeepEqual(data[key], want) {
					t.Errorf("%s = %#v, want %#v", key, data[key], want)
				}
			}
			for _, key := range []string{"issue", "pull_request"} {
				var want any
				if key == tt.subject {
					want = tt.details
				}
				if !reflect.DeepEqual(data[key], want) {
					t.Errorf("%s = %#v, want %#v", key, data[key], want)
				}
			}
			for key, value := range map[string]string{"head_ref": tt.headRef, "user_request": tt.request} {
				var want any
				if value != "" {
					want = value
				}
				if data[key] != want {
					t.Errorf("%s = %#v, want %#v", key, data[key], want)
				}
			}
		})
	}
}

func TestGitHubEventPayloads(t *testing.T) {
	for _, tt := range []struct {
		name, env, file string
		want            any
	}{
		{"env wins", `{"comment":{"body":"env"}}`, `{"comment":{"body":"file"}}`, "env"},
		{"file fallback", "", `{"comment":{"body":"file"}}`, "file"},
		{"malformed env fallback", "invalid", `{"comment":{"body":"file"}}`, "file"},
		{"malformed file", "", "invalid", nil},
		{"missing file", "", "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleanActionsEnv(t)
			t.Setenv("GITHUB_ACTIONS", "true")
			t.Setenv("GITHUB_EVENT_PAYLOAD", tt.env)
			path := filepath.Join(t.TempDir(), "event.json")
			t.Setenv("GITHUB_EVENT_PATH", path)
			if tt.file != "" {
				if err := os.WriteFile(path, []byte(tt.file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			data, err := NewGitHubActionsCollector().Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data["user_request"], tt.want) {
				t.Fatalf("request = %v, want %v", data["user_request"], tt.want)
			}
			if _, ok := data["build_url"]; ok {
				t.Error("incomplete Actions context should not produce a malformed build URL")
			}
		})
	}
}

func TestGitCollectorWithoutRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	c := NewGitCollector(".")
	data, err := c.Collect(t.Context())
	if err != nil || len(data) != 0 {
		t.Fatalf("nonrepository = %v, %v", data, err)
	}
	t.Setenv("PATH", "")
	data, err = c.Collect(t.Context())
	if err != nil || len(data) != 0 {
		t.Fatalf("missing git = %v, %v", data, err)
	}
}

func TestGitCollectorRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_TEMPLATE_DIR", t.TempDir())
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "test")
	git("config", "user.name", "Test Author")
	git("config", "user.email", "test@example.com")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", t.TempDir())
	git("commit", "--allow-empty", "-m", "fixture")
	git("remote", "add", "origin", "https://example.com/org/repo.git")
	git("tag", "v1.0.0")
	c := NewGitCollector(".")
	data, err := c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"root":   git("rev-parse", "--show-toplevel"),
		"remote": "https://example.com/org/repo.git",
		"branch": "test",
		"commit": git("rev-parse", "HEAD"),
		"tag":    "v1.0.0",
	} {
		if data[key] != want {
			t.Errorf("%s = %v, want %v", key, data[key], want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.runGitCommand(ctx, "status"); err == nil {
		t.Fatal("canceled git command succeeded")
	}
}
