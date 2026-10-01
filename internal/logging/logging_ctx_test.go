package logging

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestCtxReturnsLoggerFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	ctx := logger.WithContext(context.Background())

	Ctx(ctx).Info().Msg("context-log")

	if !strings.Contains(buf.String(), "context-log") {
		t.Fatalf("log output = %q, want to contain %q", buf.String(), "context-log")
	}
}
