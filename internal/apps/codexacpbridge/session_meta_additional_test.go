package codexacp

import (
	"strings"
	"testing"
)

func TestSessionConfigFromNewSessionMetaNilMetaReturnsClonedDefaults(t *testing.T) {
	defaults := codexAppConfig{
		Config: map[string]any{
			"shared": "default",
		},
	}

	sessionID, cfg, err := sessionConfigFromNewSessionMeta(nil, defaults)
	if err != nil {
		t.Fatalf("sessionConfigFromNewSessionMeta() error = %v", err)
	}
	if sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", sessionID)
	}
	if cfg.Config == nil {
		t.Fatal("cfg.Config = nil, want non-nil clone")
	}

	cfg.Config["shared"] = "changed"
	if got, want := defaults.Config["shared"], "default"; got != want {
		t.Fatalf("defaults.Config.shared changed to %#v, want %q", got, want)
	}
}

func TestSessionConfigFromNewSessionMetaRejectsNonObjectMeta(t *testing.T) {
	_, _, err := sessionConfigFromNewSessionMeta("not-an-object", codexAppConfig{})
	if err == nil {
		t.Fatal("sessionConfigFromNewSessionMeta() error = nil, want non-nil")
	}
	if got, want := err.Error(), "session/new _meta must be an object"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSessionConfigFromNewSessionMetaSessionIDValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		meta map[string]any
		want string
	}{
		{
			name: "session id unsupported",
			meta: map[string]any{"sessionId": 10},
			want: "session/new _meta.sessionId is not supported",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := sessionConfigFromNewSessionMeta(tc.meta, codexAppConfig{})
			if err == nil {
				t.Fatalf("sessionConfigFromNewSessionMeta() error = nil, want %q", tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestSessionConfigFromNewSessionMetaCodexObjectValidation(t *testing.T) {
	sessionID, cfg, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": nil,
	}, codexAppConfig{Sandbox: "read-only"})
	if err != nil {
		t.Fatalf("sessionConfigFromNewSessionMeta(codex=nil) error = %v", err)
	}
	if sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", sessionID)
	}
	if cfg.Sandbox != "read-only" {
		t.Fatalf("cfg.Sandbox = %q, want %q", cfg.Sandbox, "read-only")
	}

	_, _, err = sessionConfigFromNewSessionMeta(map[string]any{
		"codex": "bad",
	}, codexAppConfig{})
	if err == nil {
		t.Fatal("sessionConfigFromNewSessionMeta(codex=string) error = nil, want non-nil")
	}
	if got, want := err.Error(), "session/new _meta.codex must be an object"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSessionConfigFromNewSessionMetaRejectsInvalidEnumValues(t *testing.T) {
	_, _, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": map[string]any{
			"personality": "slow",
		},
	}, codexAppConfig{})
	if err == nil {
		t.Fatal("sessionConfigFromNewSessionMeta() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `invalid codex personality "slow"`) {
		t.Fatalf("error = %q, want invalid personality", err.Error())
	}
}

func TestOptionalMetaHelpers(t *testing.T) {
	if got, err := optionalMetaString("label", nil); err != nil || got != "" {
		t.Fatalf("optionalMetaString(nil) = (%q, %v), want (\"\", nil)", got, err)
	}
	if _, err := optionalMetaString("label", 10); err == nil {
		t.Fatal("optionalMetaString(non-string) error = nil, want non-nil")
	}
	if got, err := optionalMetaString("label", "  ok  "); err != nil || got != "ok" {
		t.Fatalf("optionalMetaString(trim) = (%q, %v), want (%q, nil)", got, err, "ok")
	}

	if got, err := optionalMetaObject("label", nil); err != nil || got != nil {
		t.Fatalf("optionalMetaObject(nil) = (%#v, %v), want (nil, nil)", got, err)
	}
	if _, err := optionalMetaObject("label", "bad"); err == nil {
		t.Fatal("optionalMetaObject(non-object) error = nil, want non-nil")
	}
	original := map[string]any{"k": "v"}
	obj, err := optionalMetaObject("label", original)
	if err != nil {
		t.Fatalf("optionalMetaObject(valid) error = %v", err)
	}
	obj["k"] = "changed"
	if original["k"] != "v" {
		t.Fatalf("optionalMetaObject did not clone map: original=%#v", original)
	}

	if got, err := optionalMetaBool("label", nil); err != nil || got != nil {
		t.Fatalf("optionalMetaBool(nil) = (%#v, %v), want (nil, nil)", got, err)
	}
	if _, err := optionalMetaBool("label", "bad"); err == nil {
		t.Fatal("optionalMetaBool(non-bool) error = nil, want non-nil")
	}
	boolValue, err := optionalMetaBool("label", true)
	if err != nil {
		t.Fatalf("optionalMetaBool(valid) error = %v", err)
	}
	if boolValue == nil || !*boolValue {
		t.Fatalf("optionalMetaBool(valid) = %#v, want pointer to true", boolValue)
	}
}

func TestSessionConfigFromNewSessionMetaCodexSandboxOverridesDefaults(t *testing.T) {
	_, cfg, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": map[string]any{
			"sandbox": testSandboxWorkspaceWrite,
		},
	}, codexAppConfig{Sandbox: "read-only"})
	if err != nil {
		t.Fatalf("sessionConfigFromNewSessionMeta() error = %v", err)
	}
	if got, want := cfg.Sandbox, testSandboxWorkspaceWrite; got != want {
		t.Fatalf("cfg.Sandbox = %q, want %q", got, want)
	}
}
