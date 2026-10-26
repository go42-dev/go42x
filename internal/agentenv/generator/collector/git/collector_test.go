package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGitCollectorWithoutRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	c := New(".")
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
	c := New(".")
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
	for _, tt := range []struct {
		name   string
		remote string
		want   string
	}{
		{
			name:   "HTTPS credentials",
			remote: "https://x-access-token:disposable-token@github.com/org/repo.git",
			want:   "https://github.com/org/repo.git",
		},
		{
			name:   "HTTP username",
			remote: "http://user@example.com:8080/org/repo.git",
			want:   "http://example.com:8080/org/repo.git",
		},
		{
			name:   "SSH shorthand",
			remote: "git@github.com:org/repo.git",
			want:   "git@github.com:org/repo.git",
		},
		{
			name:   "SSH URL",
			remote: "ssh://git@github.com/org/repo.git",
			want:   "ssh://git@github.com/org/repo.git",
		},
		{
			name:   "file URL",
			remote: "file:///tmp/repo.git",
			want:   "file:///tmp/repo.git",
		},
		{
			name:   "malformed HTTPS URL is omitted",
			remote: "https://user:bad%zz@example.com/org/repo.git",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			git("remote", "set-url", "origin", tt.remote)

			data, err := c.Collect(t.Context())

			if err != nil {
				t.Fatal(err)
			}
			if len(tt.want) == 0 {
				if _, exists := data["remote"]; exists {
					t.Fatal("invalid remote was included in context")
				}
			} else if data["remote"] != tt.want {
				t.Errorf("remote = %v, want %v", data["remote"], tt.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.runGitCommand(ctx, "status"); err == nil {
		t.Fatal("canceled git command succeeded")
	}
}
