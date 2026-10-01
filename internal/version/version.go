package version

import (
	"runtime/debug"
	"strings"
)

const devVersion = "dev"

var buildVersion string

// String returns the injected release version, the canonical Go module version,
// or dev for an unversioned build.
func String() string {
	if v := strings.TrimSpace(buildVersion); v != "" {
		return v
	}
	info, _ := debug.ReadBuildInfo()
	return moduleVersion(info)
}

func moduleVersion(info *debug.BuildInfo) string {
	if info == nil {
		return devVersion
	}
	const modulePath = "github.com/baldaworks/codex-acp"
	if info.Main.Path == modulePath {
		return releaseVersion(info.Main)
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			return releaseVersion(*dep)
		}
	}
	return devVersion
}

func releaseVersion(module debug.Module) string {
	if module.Replace != nil || module.Version == "" || module.Version == "(devel)" {
		return devVersion
	}
	return strings.TrimPrefix(module.Version, "v")
}
