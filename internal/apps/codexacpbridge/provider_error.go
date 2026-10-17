package codexacp

import (
	"math"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const (
	providerErrorQuotaExceeded          = "quota_exceeded"
	providerErrorRateLimited            = "rate_limited"
	providerErrorAuthenticationRequired = "authentication_required"
	providerErrorPaymentRequired        = "payment_required"
	providerErrorUnavailable            = "unavailable"
	providerErrorSessionExhausted       = "session_exhausted"
	providerErrorInvalidRequest         = "invalid_request"
	providerErrorUnknown                = "unknown"
	providerErrorScopeAccount           = "account"
	providerErrorScopeProvider          = "provider"
	providerErrorScopeRequest           = "request"
)

func (a *codexACPProxyAgent) providerRequestError(sessionID acp.SessionId, raw any) error {
	data := map[string]any{
		"provider_error": classifyProviderError(raw, a.sessionRateLimits(sessionID), time.Now()),
		"error":          cloneJSONValue(raw),
	}
	return &acp.RequestError{Code: -32603, Message: "Provider request failed", Data: data}
}

func providerRPCRequestError(rpcErr *appServerRPCError) error {
	raw := map[string]any{
		"code":    rpcErr.Code,
		"message": rpcErr.Message,
		"data":    cloneJSONValue(rpcErr.Data),
	}
	classifiable := raw
	if data, ok := rpcErr.Data.(map[string]any); ok {
		classifiable = cloneAnyMap(data)
	}
	providerError := classifyProviderError(classifiable, nil, time.Now())
	if providerError["kind"] == providerErrorUnknown {
		kind, scope := providerErrorKindFromRPCCode(rpcErr.Code)
		providerError["kind"] = kind
		providerError["scope"] = scope
		providerError["message"] = providerErrorMessage(kind)
	}
	return &acp.RequestError{
		Code:    -32603,
		Message: "Provider request failed",
		Data: map[string]any{
			"provider_error": providerError,
			"error":          raw,
		},
	}
}

func providerErrorKindFromRPCCode(code int) (kind, scope string) {
	switch code {
	case -32700, -32602, -32601, -32600:
		return providerErrorInvalidRequest, providerErrorScopeRequest
	case -32603, -32001:
		return providerErrorUnavailable, providerErrorScopeProvider
	default:
		return providerErrorUnknown, providerErrorScopeProvider
	}
}

func classifyProviderError(raw any, rateLimits map[string]any, now time.Time) map[string]any {
	errMap, _ := raw.(map[string]any)
	code := codexErrorCode(errMap)
	kind, scope := providerErrorKind(code, httpStatus(errMap))
	if kind == providerErrorQuotaExceeded && paymentLimitReached(rateLimits) {
		kind = providerErrorPaymentRequired
	}

	out := map[string]any{
		"provider": "codex",
		"kind":     kind,
		"scope":    scope,
		"message":  providerErrorMessage(kind),
	}
	if code != "" {
		out["code"] = code
	}
	if status := httpStatus(errMap); status != 0 {
		out["http_status"] = status
	}
	if retryAt, ok := retryAt(rateLimits, now); ok && (kind == providerErrorRateLimited || kind == providerErrorQuotaExceeded) {
		out["retry_at"] = retryAt
	}
	return out
}

func codexErrorCode(errMap map[string]any) string {
	if len(errMap) == 0 {
		return ""
	}
	for _, key := range []string{"codexErrorInfo", "code", "kind", "type"} {
		value, ok := errMap[key]
		if !ok {
			continue
		}
		switch value := value.(type) {
		case string:
			return strings.TrimSpace(value)
		case map[string]any:
			if len(value) == 1 {
				for code := range value {
					return strings.TrimSpace(code)
				}
			}
		}
	}
	return ""
}

func providerErrorKind(code string, status int) (kind, scope string) {
	switch normalizeCode(code) {
	case "usagelimitexceeded":
		return providerErrorQuotaExceeded, providerErrorScopeAccount
	case "ratelimitexceeded":
		return providerErrorRateLimited, providerErrorScopeAccount
	case "unauthorized":
		return providerErrorAuthenticationRequired, providerErrorScopeAccount
	case "flexunavailable", "serveroverloaded", "internalservererror":
		return providerErrorUnavailable, providerErrorScopeProvider
	case "httpconnectionfailed", "responsestreamconnectionfailed", "responsestreamdisconnected", "responsetoomanyfailedattempts":
		switch {
		case status == 401 || status == 403:
			return providerErrorAuthenticationRequired, providerErrorScopeAccount
		case status == 429:
			return providerErrorRateLimited, providerErrorScopeAccount
		case status >= 400 && status < 500 && status != 408:
			return providerErrorInvalidRequest, providerErrorScopeRequest
		default:
			return providerErrorUnavailable, providerErrorScopeProvider
		}
	case "sessionbudgetexceeded":
		return providerErrorSessionExhausted, "session"
	case "contextwindowexceeded", "badrequest", "sandboxerror", "cyberpolicy", "misalignmentpolicyviolation", "toomanydenials", "invalidprompt":
		return providerErrorInvalidRequest, providerErrorScopeRequest
	case "inputtoolarge", "activeturnnotsteerable", "threadrollbackfailed":
		return providerErrorInvalidRequest, providerErrorScopeRequest
	case "servershuttingdown", "connectionerror", "streamerror":
		return providerErrorUnavailable, providerErrorScopeProvider
	}
	if status == 401 || status == 403 {
		return providerErrorAuthenticationRequired, providerErrorScopeAccount
	}
	if status == 429 {
		return providerErrorRateLimited, providerErrorScopeAccount
	}
	if status == 408 || status >= 500 {
		return providerErrorUnavailable, providerErrorScopeProvider
	}
	if status >= 400 {
		return providerErrorInvalidRequest, providerErrorScopeRequest
	}
	return providerErrorUnknown, providerErrorScopeProvider
}

func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
}

func providerErrorMessage(kind string) string {
	switch kind {
	case providerErrorQuotaExceeded:
		return "Provider usage quota exceeded"
	case providerErrorRateLimited:
		return "Provider rate limit reached"
	case providerErrorAuthenticationRequired:
		return "Provider authentication required"
	case providerErrorPaymentRequired:
		return "Provider billing limit reached"
	case providerErrorUnavailable:
		return "Provider temporarily unavailable"
	case providerErrorSessionExhausted:
		return "Session budget exhausted"
	case providerErrorInvalidRequest:
		return "Provider rejected the request"
	default:
		return "Provider request failed"
	}
}

func httpStatus(errMap map[string]any) int {
	for _, key := range []string{"httpStatus", "http_status", "httpStatusCode", "statusCode", "status_code"} {
		value, ok := findJSONValue(errMap, key)
		if !ok {
			continue
		}
		if status, ok := intValue(value); ok {
			return status
		}
	}
	return 0
}

func intValue(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		if math.Trunc(value) == value && value <= math.MaxInt && value >= math.MinInt {
			return int(value), true
		}
	}
	return 0, false
}

func paymentLimitReached(rateLimits map[string]any) bool {
	for _, value := range collectJSONValues(rateLimits, "spendControlReached") {
		if reached, ok := value.(bool); ok && reached {
			return true
		}
	}
	for _, value := range collectJSONValues(rateLimits, "rateLimitReachedType") {
		typeName, _ := value.(string)
		typeName = normalizeCode(typeName)
		if strings.Contains(typeName, "credit") || strings.Contains(typeName, "spend") || strings.Contains(typeName, "payment") {
			return true
		}
	}
	return false
}

func retryAt(rateLimits map[string]any, now time.Time) (int64, bool) {
	var reset int64
	for _, value := range collectJSONValues(rateLimits, "resetsAt") {
		seconds, ok := intValue(value)
		if !ok || int64(seconds) <= now.Unix() {
			continue
		}
		candidate := int64(seconds)
		if reset != 0 && reset != candidate {
			return 0, false
		}
		reset = candidate
	}
	return reset, reset != 0
}

func findJSONValue(value any, key string) (any, bool) {
	switch value := value.(type) {
	case map[string]any:
		if found, ok := value[key]; ok && found != nil {
			return found, true
		}
		for _, nested := range value {
			if found, ok := findJSONValue(nested, key); ok {
				return found, true
			}
		}
	case []any:
		for _, nested := range value {
			if found, ok := findJSONValue(nested, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func collectJSONValues(value any, key string) []any {
	var values []any
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if found, ok := value[key]; ok && found != nil {
				values = append(values, found)
			}
			for _, nested := range value {
				walk(nested)
			}
		case []any:
			for _, nested := range value {
				walk(nested)
			}
		}
	}
	walk(value)
	return values
}

func mergeJSONMaps(current, update map[string]any) map[string]any {
	out := cloneAnyMap(current)
	if out == nil {
		out = make(map[string]any)
	}
	for key, value := range update {
		if value == nil {
			continue
		}
		nested, nestedOK := value.(map[string]any)
		previous, previousOK := out[key].(map[string]any)
		if nestedOK && previousOK {
			out[key] = mergeJSONMaps(previous, nested)
			continue
		}
		out[key] = cloneJSONValue(value)
	}
	return out
}

func (a *codexACPProxyAgent) latchTerminalError(sessionID acp.SessionId, raw any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.pendingTerminalError = cloneJSONValue(raw)
	}
}

func (a *codexACPProxyAgent) takeTerminalError(sessionID acp.SessionId) any {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return nil
	}
	raw := state.pendingTerminalError
	state.pendingTerminalError = nil
	return raw
}

func (a *codexACPProxyAgent) clearTerminalError(sessionID acp.SessionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.pendingTerminalError = nil
	}
}
