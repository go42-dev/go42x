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
			"pre-release tag",
			"v1.2.3-rc.1",
			"v1.2.3-rc.1",
		},
		{
			"pseudo-version",
			"v0.54.1-0.20261106151243-2eba5b1782a2",
			"2eba5b1782a2",
		},
		{
			"pre-release pseudo-version",
			"v1.2.3-rc.1.0.20261106151243-2eba5b1782a2",
			"2eba5b1782a2",
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

func TestCurrentSource(t *testing.T) {
	previousCommit, previousVersion := xBuildCommit, xBuildVersion
	t.Cleanup(func() {
		xBuildCommit, xBuildVersion = previousCommit, previousVersion
	})
	for _, tc := range []struct {
		name    string
		commit  string
		version string
		ref     string
		ok      bool
	}{
		{
			"release commit",
			"0123456789abcdef0123456789abcdef01234567",
			"0.55.0",
			"0123456789abcdef0123456789abcdef01234567",
			true,
		},
		{
			"version tag",
			"dev",
			"v1.2.3",
			"v1.2.3",
			true,
		},
		{
			"pseudo-version",
			"dev",
			"v0.54.1-0.20261106151243-2eba5b1782a2",
			"2eba5b1782a2",
			true,
		},
		{
			"modified checkout",
			"dev",
			"v0.54.1-0.20261106151243-2eba5b1782a2+dirty",
			"",
			false,
		},
		{
			"tag with build metadata",
			"dev",
			"v1.2.3+local",
			"",
			false,
		},
		{
			"development build",
			"dev",
			"dev",
			"",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xBuildCommit, xBuildVersion = tc.commit, tc.version
			want := Source{}
			if tc.ok {
				want = Source{
					Repository: "https://github.com/go42-dev/go42x.git",
					Ref:        tc.ref,
				}
			}

			if source, ok := CurrentSource(); source != want || ok != tc.ok {
				t.Errorf("CurrentSource() = %+v, %v; want %+v, %v", source, ok, want, tc.ok)
			}
		})
	}
}
