package buildinfo

import (
	"runtime/debug"
	"strings"
)

const ghContentURL = "https://raw.githubusercontent.com/go42-dev/go42x"

var (
	xBuildVersion string
	xBuildCommit  string
)

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

func GetCommit() string {
	if xBuildCommit != "dev" {
		return xBuildCommit
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return xBuildCommit
}

// SourceURL identifies a repository file at the binary's source revision.
// Missing revision metadata falls back to a version tag, then the master branch.
func SourceURL(path string) string {
	ref := GetCommit()
	if len(ref) == 0 || ref == "dev" {
		ref = GetVersion()
		if !strings.HasPrefix(ref, "v") {
			ref = "master"
		}
	}
	return ghContentURL + "/" + ref + "/" + path
}
