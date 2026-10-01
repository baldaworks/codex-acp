package version

import "strings"

var buildVersion = "dev"

// String returns the injected build version, or the dev fallback when unset.
func String() string {
	if v := strings.TrimSpace(buildVersion); v != "" {
		return v
	}
	return "dev"
}
