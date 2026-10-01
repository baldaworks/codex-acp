package logging

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func TestInitDefault(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if zerolog.GlobalLevel() != zerolog.InfoLevel {
		t.Errorf("expected InfoLevel, got %v", zerolog.GlobalLevel())
	}
	if slog.Default().Handler().Enabled(context.TODO(), slog.LevelDebug) {
		t.Error("expected slog level info, but debug enabled")
	}
	if !slog.Default().Handler().Enabled(context.TODO(), slog.LevelInfo) {
		t.Error("expected slog level info enabled")
	}
	if DebugEnabled() {
		t.Error("expected DebugEnabled() to be false")
	}
	if TraceEnabled() {
		t.Error("expected TraceEnabled() to be false")
	}
}

func TestInitLevelDebug(t *testing.T) {
	if err := Init(WithLevel(LevelDebug)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if zerolog.GlobalLevel() != zerolog.DebugLevel {
		t.Errorf("expected DebugLevel, got %v", zerolog.GlobalLevel())
	}
	if !slog.Default().Handler().Enabled(context.TODO(), slog.LevelDebug) {
		t.Error("expected slog level debug enabled")
	}
	if !DebugEnabled() {
		t.Error("expected DebugEnabled() to be true")
	}
	if TraceEnabled() {
		t.Error("expected TraceEnabled() to be false")
	}
}

func TestInitLevelTrace(t *testing.T) {
	if err := Init(WithLevel(LevelTrace)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if zerolog.GlobalLevel() != zerolog.TraceLevel {
		t.Errorf("expected TraceLevel, got %v", zerolog.GlobalLevel())
	}
	if !slog.Default().Handler().Enabled(context.TODO(), slog.LevelDebug-4) {
		t.Error("expected slog level trace enabled")
	}
	if !DebugEnabled() {
		t.Error("expected DebugEnabled() to be true at trace level")
	}
	if !TraceEnabled() {
		t.Error("expected TraceEnabled() to be true")
	}
}

func TestInitLevelWarn(t *testing.T) {
	if err := Init(WithLevel(LevelWarn)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if zerolog.GlobalLevel() != zerolog.WarnLevel {
		t.Errorf("expected WarnLevel, got %v", zerolog.GlobalLevel())
	}
	if slog.Default().Handler().Enabled(context.TODO(), slog.LevelInfo) {
		t.Error("expected slog level warn, but info enabled")
	}
	if !slog.Default().Handler().Enabled(context.TODO(), slog.LevelWarn) {
		t.Error("expected slog level warn enabled")
	}
}

func TestInitLevelError(t *testing.T) {
	if err := Init(WithLevel(LevelError)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if zerolog.GlobalLevel() != zerolog.ErrorLevel {
		t.Errorf("expected ErrorLevel, got %v", zerolog.GlobalLevel())
	}
	if slog.Default().Handler().Enabled(context.TODO(), slog.LevelWarn) {
		t.Error("expected slog level error, but warn enabled")
	}
	if !slog.Default().Handler().Enabled(context.TODO(), slog.LevelError) {
		t.Error("expected slog level error enabled")
	}
}

func TestInitLevelWarningAlias(t *testing.T) {
	if err := Init(WithLevel("warning")); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if zerolog.GlobalLevel() != zerolog.WarnLevel {
		t.Errorf("expected WarnLevel, got %v", zerolog.GlobalLevel())
	}
}

func TestInitInvalidLevel(t *testing.T) {
	if err := Init(WithLevel("nope")); err == nil {
		t.Fatal("Init() error = nil, want invalid level error")
	}
}

func TestDebugEnabled(t *testing.T) {
	if err := Init(WithLevel(LevelDebug)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !DebugEnabled() {
		t.Error("expected DebugEnabled() to be true when level=debug")
	}

	if err := Init(WithLevel(LevelTrace)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !DebugEnabled() {
		t.Error("expected DebugEnabled() to be true when level=trace")
	}

	if err := Init(WithLevel(LevelInfo)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if DebugEnabled() {
		t.Error("expected DebugEnabled() to be false when level=info")
	}
}

func TestTraceEnabled(t *testing.T) {
	if err := Init(WithLevel(LevelTrace)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !TraceEnabled() {
		t.Error("expected TraceEnabled() to be true when level=trace")
	}

	if err := Init(WithLevel(LevelDebug)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if TraceEnabled() {
		t.Error("expected TraceEnabled() to be false when level=debug")
	}
}

func TestJSONEnabled(t *testing.T) {
	if err := Init(WithLevel(LevelInfo), WithJson(true)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
}

func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	os.Exit(m.Run())
}
