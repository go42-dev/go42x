package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var (
	binaryPath    string
	binaryVersion = "dev"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	if dist := os.Getenv("GO42X_RELEASE_DIST"); dist != "" {
		if os.Getenv("GO42X_E2E_BINARY") != "" || os.Getenv("GO42X_E2E_VERSION") != "" {
			fmt.Fprintln(os.Stderr, "GO42X_RELEASE_DIST cannot be combined with GO42X_E2E_BINARY or GO42X_E2E_VERSION")
			return 1
		}
		return runReleaseTests(m, dist)
	}
	if path := os.Getenv("GO42X_E2E_BINARY"); path != "" {
		// Release tests must use the supplied binary; never silently rebuild it.
		if !filepath.IsAbs(path) || os.Getenv("GO42X_E2E_VERSION") == "" {
			fmt.Fprintln(os.Stderr, "GO42X_E2E_BINARY must be absolute and requires GO42X_E2E_VERSION")
			return 1
		}
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read E2E binary:", err)
			return 1
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			fmt.Fprintln(os.Stderr, "GO42X_E2E_BINARY must be a regular executable file")
			return 1
		}
		binaryPath, binaryVersion = path, os.Getenv("GO42X_E2E_VERSION")
		return m.Run()
	}
	if os.Getenv("GO42X_E2E_VERSION") != "" {
		fmt.Fprintln(os.Stderr, "GO42X_E2E_VERSION requires GO42X_E2E_BINARY")
		return 1
	}

	dir, err := os.MkdirTemp("", "go42x-e2e-binary-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	binaryPath = filepath.Join(dir, "go42x")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	// Build once for the entire suite. The child CLI also gets race detection.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-race", "-trimpath", "-buildvcs=false",
		"-o", binaryPath, ".")
	build.Dir = filepath.Join("..", "..")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build E2E binary:", err)
		return 1
	}
	return m.Run()
}

type project struct {
	root string
	env  []string
}

func newProject(t *testing.T) *project {
	t.Helper()
	root, userDir, tempDir := t.TempDir(), t.TempDir(), t.TempDir()
	return &project{
		root: root,
		// Do not inherit developer credentials, GO42X flags, CI metadata, or tools.
		env: []string{
			"PATH=" + filepath.Dir(binaryPath),
			"HOME=" + userDir,
			"USERPROFILE=" + userDir,
			"XDG_CONFIG_HOME=" + userDir,
			"XDG_CACHE_HOME=" + userDir,
			"TMPDIR=" + tempDir,
			"TEMP=" + tempDir,
			"TMP=" + tempDir,
			"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
			"CI=false",
			"GITHUB_ACTIONS=false",
			"GORACE=halt_on_error=1 atexit_sleep_ms=0",
		},
	}
}

func (p *project) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir, cmd.Env = p.root, p.env
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

type commandResult struct {
	stdout, stderr string
}

func (p *project) run(t *testing.T, wantExit int, args ...string) commandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := p.command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("go42x %v timed out: %v\nstdout: %s\nstderr: %s", args, ctx.Err(), &stdout, &stderr)
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("execute go42x %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	if code != wantExit {
		t.Fatalf("go42x %v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, wantExit, &stdout, &stderr)
	}
	return commandResult{stdout: stdout.String(), stderr: stderr.String()}
}

func (p *project) write(t *testing.T, name, content string) {
	t.Helper()
	path := filepath.Join(p.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func (p *project) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (p *project) configure(t *testing.T) {
	t.Helper()
	p.run(t, 0, "agentenv", "init")
	data, err := os.ReadFile("testdata/go42x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p.write(t, ".go42x/go42x.yaml", string(data))
	p.write(t, ".go42x/agents.tpl.md", "# {{ .project.name }}\n")
}

type fileState struct {
	content string
	mode    fs.FileMode
}

func (p *project) snapshot(t *testing.T) map[string]fileState {
	t.Helper()
	files := make(map[string]fileState)
	err := filepath.WalkDir(p.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(p.root, path)
		if err != nil {
			return err
		}
		state := fileState{mode: info.Mode()}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.content = string(data)
		}
		files[relative] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func (p *project) assertUnchanged(t *testing.T, before map[string]fileState) {
	t.Helper()
	after := p.snapshot(t)
	if maps.Equal(before, after) {
		return
	}
	for path, state := range before {
		if current, ok := after[path]; !ok || current != state {
			t.Errorf("file contents, permissions, or existence changed: %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("unexpected file or directory created: %s", path)
		}
	}
}
