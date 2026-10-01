package command

import (
	"testing"

	"github.com/baldaworks/codex-acp/pkg/cobracmd"
)

func TestCommandDelegatesToPublicCobraCommand(t *testing.T) {
	got := Command()
	want := cobracmd.New()

	if got.Use != want.Use {
		t.Fatalf("Use = %q, want %q", got.Use, want.Use)
	}
	if got.Short != want.Short {
		t.Fatalf("Short = %q, want %q", got.Short, want.Short)
	}
	if got.Flags().Lookup("sandbox") == nil {
		t.Fatal("sandbox flag missing")
	}
	if got.Flags().Lookup("codex-args") == nil {
		t.Fatal("codex-args flag missing")
	}
	if got.Commands() == nil || len(got.Commands()) == 0 {
		t.Fatal("subcommands missing")
	}
}
