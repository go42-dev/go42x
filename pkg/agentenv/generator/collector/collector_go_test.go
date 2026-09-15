package collector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Reuse the native test binary as Go so subprocess checks work without a shell.
func TestMain(m *testing.M) {
	switch os.Getenv("GO42X_TEST_GO_COLLECTOR") {
	case "env":
		if len(os.Args) < 4 || !slices.Equal(os.Args[1:4], []string{"env", "-json", "--"}) {
			os.Exit(2)
		}
		seen := make(map[string]bool)
		for _, name := range os.Args[4:] {
			if seen[name] {
				os.Exit(3)
			}
			seen[name] = true
		}
		wd, err := os.Getwd()
		if err != nil {
			os.Exit(4)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{
			"GOVERSION": "go1.27.9", "GOTOOLCHAIN": "auto", "GOMOD": filepath.Join(wd, "go.mod"),
			"CGO_ENABLED": "0", "GOFLAGS": "-tags=example", "GOBIN": "", "UNREQUESTED": "private-value",
		}); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	case "invalid":
		_, _ = os.Stdout.WriteString("invalid JSON")
		os.Exit(0)
	case "exit":
		_, _ = os.Stderr.WriteString("private-value")
		os.Exit(1)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeGo(t *testing.T, mode string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.Link(binary, path); err != nil {
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("GO42X_TEST_GO_COLLECTOR", mode)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
}

func TestGoCollectorEffectiveEnvironment(t *testing.T) {
	fakeGo(t, "env")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\ngo 1.27\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	data, err := NewGoCollector(root, []string{"GOFLAGS", "GOVERSION", "GOFLAGS"}).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// macOS may resolve /var to /private/var in the child's working directory.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	env := data["env"].(map[string]string)
	env["GOMOD"], err = filepath.EvalSymlinks(env["GOMOD"])
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"go_version": "1.27",
		"env": map[string]string{
			"GOVERSION": "go1.27.9", "GOTOOLCHAIN": "auto", "GOMOD": filepath.Join(resolved, "go.mod"),
			"CGO_ENABLED": "0", "GOFLAGS": "-tags=example",
		},
	}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("Collect() = %+v, want %+v", data, want)
	}
}

func TestGoCollectorModuleWithoutGo(t *testing.T) {
	t.Setenv("PATH", "")
	for _, tt := range []struct{ name, content, want string }{
		{"missing", "", ""},
		{"requirement", "module example\ngo 1.27\n", "1.27"},
		{"patch and comment", "module example\n\tgo\t1.27.2 // requirement\ntoolchain go1.28.1\n", "1.27.2"},
		{"toolchain only", "module example\ntoolchain go1.28.1\n", ""},
		{"invalid", "module example\ngo invalid\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.content != "" {
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			data, err := NewGoCollector(root, nil).Collect(t.Context())
			if !errors.Is(err, exec.ErrNotFound) {
				t.Fatalf("Collect() error = %v, want missing Go", err)
			}
			want := make(map[string]any)
			if tt.want != "" {
				want["go_version"] = tt.want
			}
			if !reflect.DeepEqual(data, want) {
				t.Fatalf("Collect() = %+v, want %+v", data, want)
			}
		})
	}
}

func TestGoCollectorCommandFailures(t *testing.T) {
	for _, mode := range []string{"invalid", "exit", "wait", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			fakeGo(t, mode)
			root := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(root, "go.mod"),
				[]byte("module example\ngo 1.27\n"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			c := NewGoCollector(root, nil)
			ctx := t.Context()
			if mode == "wait" {
				c.timeout = 200 * time.Millisecond
			}
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			start := time.Now()
			data, err := c.Collect(ctx)
			if err == nil || strings.Contains(err.Error(), "private-value") ||
				!reflect.DeepEqual(data, map[string]any{"go_version": "1.27"}) {
				t.Fatalf("Collect() = %+v, %v; want only the module requirement and a non-sensitive error", data, err)
			}
			if mode == "wait" && (!errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second) {
				t.Fatalf("command did not respect timeout: %v (%v)", err, time.Since(start))
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("command did not respect cancellation: %v", err)
			}
		})
	}
}

func TestGoCollectorKeepsEnvWhenModuleUnreadable(t *testing.T) {
	fakeGo(t, "env")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "go.mod"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := NewGoCollector(root, nil).Collect(t.Context())
	if err == nil || !strings.Contains(err.Error(), "read project go.mod") || data["go_version"] != nil {
		t.Fatalf("Collect() = %+v, %v; want module read error", data, err)
	}
	if env, ok := data["env"].(map[string]string); !ok || env["GOVERSION"] != "go1.27.9" || env["GOFLAGS"] != "" {
		t.Fatalf("Collect() did not preserve requested environment: %+v", data)
	}
}

func TestGoCollectorWithInstalledGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go is not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\ngo 1.18\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOFLAGS": "-tags=example",
		"GOPATH": filepath.Join(root, "gopath"), "GOCACHE": filepath.Join(root, "cache"), "CGO_ENABLED": "0",
	} {
		t.Setenv(name, value)
	}
	t.Chdir(t.TempDir())
	data, err := NewGoCollector(root, []string{"GOFLAGS"}).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	env := data["env"].(map[string]string)
	if data["go_version"] != "1.18" || !strings.HasPrefix(env["GOVERSION"], "go") || env["GOROOT"] == "" {
		t.Fatalf("missing module/toolchain context: %+v", data)
	}
	for _, key := range []string{"GOTOOLCHAIN", "GOWORK", "GOFLAGS", "GOPATH", "GOCACHE", "CGO_ENABLED"} {
		if env[key] != os.Getenv(key) {
			t.Errorf("%s = %q, want effective setting %q", key, env[key], os.Getenv(key))
		}
	}
	module, err := os.Stat(env["GOMOD"])
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(filepath.Join(root, "go.mod"))
	if err != nil || !os.SameFile(module, want) {
		t.Fatalf("go env inspected the wrong module: %q, %v", env["GOMOD"], err)
	}
}
