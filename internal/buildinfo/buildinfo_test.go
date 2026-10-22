package buildinfo

import (
	"strings"
	"testing"
)

func TestSourceURLPinsBuildCommit(t *testing.T) {
	previousCommit, previousVersion := xBuildCommit, xBuildVersion
	t.Cleanup(func() {
		xBuildCommit, xBuildVersion = previousCommit, previousVersion
	})
	xBuildCommit = "0123456789abcdef0123456789abcdef01234567"
	xBuildVersion = "v1.2.3"
	for _, path := range []string{"assets/schemas/go42x.schema.yaml", "assets/schemas/vendor.schema.yaml"} {
		want := "https://raw.githubusercontent.com/go42-dev/go42x/0123456789abcdef0123456789abcdef01234567/" + path
		if got := SourceURL(path); got != want {
			t.Errorf("SourceURL(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSourceURLFallbacks(t *testing.T) {
	previousCommit, previousVersion := xBuildCommit, xBuildVersion
	t.Cleanup(func() {
		xBuildCommit, xBuildVersion = previousCommit, previousVersion
	})
	xBuildCommit = ""
	const path = "assets/schemas/go42x.schema.yaml"
	for _, tc := range []struct {
		name    string
		version string
		ref     string
	}{
		{
			"version tag",
			"v1.2.3",
			"v1.2.3",
		},
		{
			"unversioned build",
			"snapshot",
			"master",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xBuildVersion = tc.version
			want := "https://raw.githubusercontent.com/go42-dev/go42x/" + tc.ref + "/" + path
			if got := SourceURL(path); got != want {
				t.Errorf("SourceURL(%q) = %q, want %q", path, got, want)
			}
		})
	}
}

func TestSourceURLDevelopmentBuild(t *testing.T) {
	previousCommit, previousVersion := xBuildCommit, xBuildVersion
	t.Cleanup(func() {
		xBuildCommit, xBuildVersion = previousCommit, previousVersion
	})
	xBuildCommit, xBuildVersion = "dev", "dev"
	if got := SourceURL("assets/schemas/go42x.schema.yaml"); strings.Contains(got, "/go42x/dev/") {
		t.Errorf("SourceURL() used the development marker as a source revision: %q", got)
	}
}
