package environment

import (
	"os"
	"runtime"
	"testing"
)

func TestEnvironmentCollector(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CI", "true")
	t.Setenv("AGENTENV_TEST_INCLUDED", "yes")
	t.Setenv("AGENTENV_TEST_EMPTY", "")
	t.Setenv("AGENTENV_TEST_PRIVATE", "secret")
	data, err := New(
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
	data, err = New(nil, ".").Collect(t.Context())
	if err != nil || data["variables"] != nil || data["is_ci"] != false {
		t.Fatalf("unconfigured environment variables = %v, %v", data, err)
	}
}
