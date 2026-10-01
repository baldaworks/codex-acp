package version

import (
	"runtime/debug"
	"testing"
)

func TestStringDefaultsToDev(t *testing.T) {
	orig := buildVersion
	t.Cleanup(func() {
		buildVersion = orig
	})

	buildVersion = ""
	if got := String(); got != devVersion {
		t.Fatalf("String() = %q, want %q", got, devVersion)
	}
}

func TestStringReturnsInjectedVersion(t *testing.T) {
	orig := buildVersion
	t.Cleanup(func() {
		buildVersion = orig
	})

	buildVersion = "1.5.6"
	if got := String(); got != "1.5.6" {
		t.Fatalf("String() = %q, want %q", got, "1.5.6")
	}
}

func TestModuleVersion(t *testing.T) {
	const modulePath = "github.com/baldaworks/codex-acp"
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "missing info", want: devVersion},
		{name: "canonical executable", info: &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.9.3"}}, want: "1.9.3"},
		{name: "development executable", info: &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}, want: devVersion},
		{name: "legacy executable", info: &debug.BuildInfo{Main: debug.Module{Path: "github.com/normahq/codex-acp-bridge", Version: "v1.9.4"}, Deps: []*debug.Module{{Path: modulePath, Version: "v1.9.3"}}}, want: "1.9.3"},
		{name: "local replacement", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: modulePath, Version: "v1.9.3", Replace: &debug.Module{Path: "../codex-acp"}}}}, want: devVersion},
		{name: "unrelated executable", info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v2.0.0"}}, want: devVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := moduleVersion(test.info); got != test.want {
				t.Fatalf("moduleVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
