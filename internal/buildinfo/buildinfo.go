package buildinfo

import (
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const repositoryURL = "https://github.com/go42-dev/go42x.git"

const ghContentURL = "https://raw.githubusercontent.com/go42-dev/go42x"

var (
	xBuildVersion string
	xBuildCommit  string
)

// Source identifies a go42x revision: a Git repository and a commit or tag in it.
type Source struct {
	Repository string `yaml:"repository"`
	Ref        string `yaml:"ref"`
}

func init() {
	if len(xBuildVersion) == 0 {
		xBuildVersion = "dev"
	}
	if len(xBuildCommit) == 0 {
		xBuildCommit = "dev"
	}
}

func GetVersion() string {
	if xBuildVersion != "dev" {
		return xBuildVersion
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "(devel)" && len(info.Main.Version) > 0 {
			return info.Main.Version
		}
	}
	return xBuildVersion
}

// CurrentSource identifies the revision that the binary was built from. It reports false
// when no published revision matches the binary's sources, such as for a build from a
// working tree with uncommitted changes.
func CurrentSource() (Source, bool) {
	ref, ok := sourceRef()
	if !ok {
		return Source{}, false
	}

	return Source{
		Repository: repositoryURL,
		Ref:        ref,
	}, true
}

// sourceRef returns the commit or release tag of the binary's sources: the release
// commit, the checkout's commit, the commit in a Go pseudo-version, or a version tag.
func sourceRef() (string, bool) {
	if len(xBuildCommit) > 0 && xBuildCommit != "dev" {
		return xBuildCommit, true
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		var modified bool
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
		if modified {
			return "", false
		}
		if len(revision) > 0 {
			return revision, true
		}
	}

	version := GetVersion()
	switch {
	case strings.HasSuffix(version, "+dirty"):
		return "", false
	case module.IsPseudoVersion(version):
		// GitHub and Git resolve commits, but not Go pseudo-versions.
		revision, err := module.PseudoVersionRev(version)
		return revision, err == nil
	case semver.IsValid(version) && len(semver.Build(version)) == 0:
		return version, true
	default:
		return "", false
	}
}

// SourceURL identifies a repository file at the binary's source revision. Builds without
// a published revision link to the master branch.
func SourceURL(path string) string {
	ref, ok := sourceRef()
	if !ok {
		ref = "master"
	}
	return ghContentURL + "/" + ref + "/" + path
}
