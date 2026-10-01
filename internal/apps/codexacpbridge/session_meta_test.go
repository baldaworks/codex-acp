package codexacp

import (
	"testing"
)

func TestSessionConfigFromNewSessionMetaParsesCodexOverrides(t *testing.T) {
	defaultCfg := codexAppConfig{
		Sandbox:           "read-only",
		ApprovalPolicy:    "never",
		ApprovalsReviewer: "user",
		Profile:           "default-profile",
		CompactPrompt:     "default-compact",
		Config: map[string]any{
			"shared": "default",
		},
	}

	_, cfg, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": map[string]any{
			"sandbox":               "workspace-write",
			"approvalPolicy":        "on-request",
			"approvalsReviewer":     "guardian_subagent",
			"baseInstructions":      "base",
			"developerInstructions": "dev",
			"profile":               "meta-profile",
			"compactPrompt":         "meta-compact",
			"modelProvider":         "openai",
			"personality":           "pragmatic",
			"serviceTier":           "flex",
			"ephemeral":             true,
			"config": map[string]any{
				"shared": "meta",
				"k":      "v",
			},
		},
	}, defaultCfg)
	if err != nil {
		t.Fatalf("sessionConfigFromNewSessionMeta() error = %v", err)
	}
	if cfg.Sandbox != "workspace-write" {
		t.Fatalf("cfg.Sandbox = %q, want %q", cfg.Sandbox, "workspace-write")
	}
	if cfg.ApprovalPolicy != "on-request" {
		t.Fatalf("cfg.ApprovalPolicy = %q, want %q", cfg.ApprovalPolicy, "on-request")
	}
	if cfg.ApprovalsReviewer != "guardian_subagent" {
		t.Fatalf("cfg.ApprovalsReviewer = %q, want %q", cfg.ApprovalsReviewer, "guardian_subagent")
	}
	if cfg.BaseInstructions != "base" {
		t.Fatalf("cfg.BaseInstructions = %q, want %q", cfg.BaseInstructions, "base")
	}
	if cfg.DeveloperInstructions != "dev" {
		t.Fatalf("cfg.DeveloperInstructions = %q, want %q", cfg.DeveloperInstructions, "dev")
	}
	if cfg.Profile != "meta-profile" {
		t.Fatalf("cfg.Profile = %q, want %q", cfg.Profile, "meta-profile")
	}
	if cfg.CompactPrompt != "meta-compact" {
		t.Fatalf("cfg.CompactPrompt = %q, want %q", cfg.CompactPrompt, "meta-compact")
	}
	if cfg.ModelProvider != "openai" {
		t.Fatalf("cfg.ModelProvider = %q, want %q", cfg.ModelProvider, "openai")
	}
	if cfg.Personality != "pragmatic" {
		t.Fatalf("cfg.Personality = %q, want %q", cfg.Personality, "pragmatic")
	}
	if cfg.ServiceTier != "flex" {
		t.Fatalf("cfg.ServiceTier = %q, want %q", cfg.ServiceTier, "flex")
	}
	if cfg.Ephemeral == nil || !*cfg.Ephemeral {
		t.Fatalf("cfg.Ephemeral = %#v, want true", cfg.Ephemeral)
	}
	if cfg.Config == nil {
		t.Fatal("cfg.Config = nil, want non-nil")
	}
	if got := cfg.Config["shared"]; got != "meta" {
		t.Fatalf("cfg.Config.shared = %#v, want %q", got, "meta")
	}
	if got := cfg.Config["k"]; got != "v" {
		t.Fatalf("cfg.Config.k = %#v, want %q", got, "v")
	}
}

func TestSessionConfigFromNewSessionMetaRejectsUnknownCodexKey(t *testing.T) {
	_, _, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": map[string]any{
			"unsupported": true,
		},
	}, codexAppConfig{})
	if err == nil {
		t.Fatal("sessionConfigFromNewSessionMeta() error = nil, want non-nil")
	}
	if got, want := err.Error(), "session/new _meta.codex.unsupported is not supported"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSessionConfigFromNewSessionMetaRejectsInvalidType(t *testing.T) {
	_, _, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": map[string]any{
			"sandbox": 1,
		},
	}, codexAppConfig{})
	if err == nil {
		t.Fatal("sessionConfigFromNewSessionMeta() error = nil, want non-nil")
	}
	if got, want := err.Error(), "session/new _meta.codex.sandbox must be a string"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSessionConfigFromNewSessionMetaRejectsInvalidEnum(t *testing.T) {
	_, _, err := sessionConfigFromNewSessionMeta(map[string]any{
		"codex": map[string]any{
			"serviceTier": "slow",
		},
	}, codexAppConfig{})
	if err == nil {
		t.Fatal("sessionConfigFromNewSessionMeta() error = nil, want non-nil")
	}
	if got, want := err.Error(), "invalid codex service tier \"slow\""; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
