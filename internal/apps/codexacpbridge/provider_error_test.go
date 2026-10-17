package codexacp

import (
	"errors"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestProviderRPCRequestErrorPreservesData(t *testing.T) {
	err := providerRPCRequestError(&appServerRPCError{
		Code:    -32000,
		Message: "turn start rejected",
		Data:    map[string]any{"code": "input_too_large", "limit": float64(1_000)},
	})
	var reqErr *acp.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("error type = %T, want *acp.RequestError", err)
	}
	data, ok := reqErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("RequestError.Data type = %T, want map[string]any", reqErr.Data)
	}
	providerError := mapValue(data, "provider_error")
	if providerError["kind"] != providerErrorInvalidRequest || providerError["scope"] != "request" {
		t.Errorf("provider_error = %#v, want invalid request", providerError)
	}
	raw := mapValue(data, "error")
	rawData := mapValue(raw, "data")
	if rawData["limit"] != float64(1_000) {
		t.Errorf("raw error data = %#v, want preserved limit", rawData)
	}
}

func TestProviderRPCRequestErrorClassifiesCodeOnlyErrors(t *testing.T) {
	tests := []struct {
		name      string
		code      int
		message   string
		wantKind  string
		wantScope string
	}{
		{name: "server overloaded", code: -32001, message: "Server overloaded; retry later.", wantKind: providerErrorUnavailable, wantScope: providerErrorScopeProvider},
		{name: "invalid params", code: -32602, message: "Invalid params", wantKind: providerErrorInvalidRequest, wantScope: providerErrorScopeRequest},
		{name: "future code", code: -32123, message: "future", wantKind: providerErrorUnknown, wantScope: providerErrorScopeProvider},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := providerRPCRequestError(&appServerRPCError{Code: test.code, Message: test.message})
			var reqErr *acp.RequestError
			if !errors.As(err, &reqErr) {
				t.Fatalf("error type = %T, want *acp.RequestError", err)
			}
			data, _ := reqErr.Data.(map[string]any)
			providerError := mapValue(data, "provider_error")
			if providerError["kind"] != test.wantKind || providerError["scope"] != test.wantScope {
				t.Errorf("provider_error = %#v, want %s/%s", providerError, test.wantKind, test.wantScope)
			}
		})
	}
}

func TestClassifyProviderError(t *testing.T) {
	now := time.Unix(1_000, 0)
	tests := []struct {
		name       string
		raw        map[string]any
		rateLimits map[string]any
		wantKind   string
		wantScope  string
		wantRetry  int64
	}{
		{
			name:      "quota",
			raw:       map[string]any{"codexErrorInfo": "usageLimitExceeded"},
			wantKind:  providerErrorQuotaExceeded,
			wantScope: "account",
		},
		{
			name: "quota refined to payment",
			raw:  map[string]any{"codexErrorInfo": map[string]any{"usageLimitExceeded": map[string]any{}}},
			rateLimits: map[string]any{
				"rateLimitReachedType": "ownerCredits",
			},
			wantKind:  providerErrorPaymentRequired,
			wantScope: "account",
		},
		{
			name: "rate limit with reset",
			raw:  map[string]any{"codexErrorInfo": "rateLimitExceeded", "httpStatus": float64(429)},
			rateLimits: map[string]any{
				"primary": map[string]any{"resetsAt": float64(1_500)},
			},
			wantKind:  providerErrorRateLimited,
			wantScope: "account",
			wantRetry: 1_500,
		},
		{
			name: "ambiguous reset omitted",
			raw:  map[string]any{"codexErrorInfo": "rateLimitExceeded"},
			rateLimits: map[string]any{
				"primary":   map[string]any{"resetsAt": float64(1_500)},
				"secondary": map[string]any{"resetsAt": float64(1_800)},
			},
			wantKind:  providerErrorRateLimited,
			wantScope: "account",
		},
		{
			name: "authentication by status",
			raw: map[string]any{"codexErrorInfo": map[string]any{
				"httpConnectionFailed": map[string]any{"httpStatusCode": float64(401)},
			}},
			wantKind:  providerErrorAuthenticationRequired,
			wantScope: "account",
		},
		{
			name: "stream unavailable",
			raw: map[string]any{"codexErrorInfo": map[string]any{
				"responseStreamDisconnected": map[string]any{"httpStatusCode": nil},
			}},
			wantKind:  providerErrorUnavailable,
			wantScope: "provider",
		},
		{
			name: "response retries exhausted by rate limit",
			raw: map[string]any{"codexErrorInfo": map[string]any{
				"responseTooManyFailedAttempts": map[string]any{"httpStatusCode": float64(429)},
			}},
			wantKind:  providerErrorRateLimited,
			wantScope: "account",
		},
		{
			name:      "cyber policy",
			raw:       map[string]any{"codexErrorInfo": "cyberPolicy"},
			wantKind:  providerErrorInvalidRequest,
			wantScope: "request",
		},
		{
			name:      "session budget",
			raw:       map[string]any{"codexErrorInfo": "sessionBudgetExceeded"},
			wantKind:  providerErrorSessionExhausted,
			wantScope: "session",
		},
		{
			name:      "bad request",
			raw:       map[string]any{"codexErrorInfo": "contextWindowExceeded"},
			wantKind:  providerErrorInvalidRequest,
			wantScope: "request",
		},
		{
			name:      "future variant",
			raw:       map[string]any{"codexErrorInfo": map[string]any{"futureVariant": map[string]any{"secret": "kept raw only"}}},
			wantKind:  providerErrorUnknown,
			wantScope: "provider",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyProviderError(test.raw, test.rateLimits, now)
			if got["kind"] != test.wantKind {
				t.Errorf("kind = %v, want %q", got["kind"], test.wantKind)
			}
			if got["scope"] != test.wantScope {
				t.Errorf("scope = %v, want %q", got["scope"], test.wantScope)
			}
			if gotRetry, _ := got["retry_at"].(int64); gotRetry != test.wantRetry {
				t.Errorf("retry_at = %v, want %d", got["retry_at"], test.wantRetry)
			}
			if message, _ := got["message"].(string); message == "" || message == "kept raw only" {
				t.Errorf("message = %q, want bounded safe text", message)
			}
		})
	}
}

func TestMergeJSONMapsPreservesSparseState(t *testing.T) {
	current := map[string]any{
		"primary": map[string]any{
			"usedPercent": float64(100),
			"resetsAt":    float64(2_000),
		},
		"rateLimitReachedType": "rateLimit",
	}
	update := map[string]any{
		"primary": map[string]any{
			"usedPercent": float64(80),
			"resetsAt":    nil,
		},
	}

	got := mergeJSONMaps(current, update)
	primary := mapValue(got, "primary")
	if primary["usedPercent"] != float64(80) {
		t.Errorf("usedPercent = %v, want 80", primary["usedPercent"])
	}
	if primary["resetsAt"] != float64(2_000) {
		t.Errorf("resetsAt = %v, want preserved 2000", primary["resetsAt"])
	}
	if got["rateLimitReachedType"] != "rateLimit" {
		t.Errorf("rateLimitReachedType = %v, want preserved rateLimit", got["rateLimitReachedType"])
	}
}
