package codexacp

import (
	"fmt"
	"strings"
)

var (
	validMCPApprovalPolicies = map[string]struct{}{
		string(MCPApprovalPolicyAsk):   {},
		string(MCPApprovalPolicyAllow): {},
		string(MCPApprovalPolicyDeny):  {},
	}
	validCodexSandboxModes = map[string]struct{}{
		"read-only":          {},
		"workspace-write":    {},
		"danger-full-access": {},
	}
	validCodexApprovalPolicies = map[string]struct{}{
		"untrusted":  {},
		"on-failure": {},
		"on-request": {},
		"never":      {},
	}
	validCodexApprovalsReviewers = map[string]struct{}{
		"user":              {},
		"guardian_subagent": {},
	}
	validCodexPersonalities = map[string]struct{}{
		"none":      {},
		"friendly":  {},
		"pragmatic": {},
	}
	validCodexServiceTiers = map[string]struct{}{
		"fast": {},
		"flex": {},
	}
	validReasoningThoughtModes = map[string]struct{}{
		reasoningThoughtsOff:     {},
		reasoningThoughtsSummary: {},
		reasoningThoughtsContent: {},
		reasoningThoughtsBoth:    {},
	}
	validReasoningSummaries = map[string]struct{}{
		reasoningSummaryAuto:     {},
		reasoningSummaryConcise:  {},
		reasoningSummaryDetailed: {},
		reasoningSummaryNone:     {},
	}
)

const (
	// MCPApprovalPolicy controls how the bridge settles MCP tool-call approvals.
	MCPApprovalPolicyAsk   MCPApprovalPolicy = "ask"
	MCPApprovalPolicyAllow MCPApprovalPolicy = "allow"
	MCPApprovalPolicyDeny  MCPApprovalPolicy = "deny"

	defaultMCPApprovalPolicy = MCPApprovalPolicyAsk

	reasoningThoughtsOff     = "off"
	reasoningThoughtsSummary = "summary"
	reasoningThoughtsContent = "content"
	reasoningThoughtsBoth    = "both"
	defaultReasoningThoughts = reasoningThoughtsSummary
	reasoningSummaryAuto     = "auto"
	reasoningSummaryConcise  = "concise"
	reasoningSummaryDetailed = "detailed"
	reasoningSummaryNone     = "none"
	defaultReasoningSummary  = reasoningSummaryAuto
)

// MCPApprovalPolicy is the process-wide policy for MCP tool-call approvals.
type MCPApprovalPolicy string

// Options configures Codex bridge backend -> ACP proxy behavior.
type Options struct {
	Name               string
	DeferBackend       bool
	MessageStreaming   bool
	ReasoningStreaming bool
	ReasoningThoughts  string
	ReasoningSummary   string
	CodexArgs          []string
	MCPApprovalPolicy  MCPApprovalPolicy
	Sandbox            string

	reasoningStreamingConfigured bool
}

type codexAppConfig struct {
	ApprovalPolicy        string
	ApprovalsReviewer     string
	BaseInstructions      string
	CompactPrompt         string
	Config                map[string]any
	DeveloperInstructions string
	Ephemeral             *bool
	Model                 string
	ModelProvider         string
	Personality           string
	Profile               string
	Sandbox               string
	ServiceTier           string
}

func (c codexAppConfig) withModel(model string) codexAppConfig {
	next := c
	nextModel := strings.TrimSpace(model)
	if nextModel != "" {
		next.Model = nextModel
	}
	return next
}

func (c codexAppConfig) clone() codexAppConfig {
	next := c
	next.Config = cloneMap(c.Config)
	if c.Ephemeral != nil {
		ephemeral := *c.Ephemeral
		next.Ephemeral = &ephemeral
	}
	return next
}

func (o Options) appConfig() codexAppConfig {
	return codexAppConfig{
		Sandbox: strings.TrimSpace(o.Sandbox),
	}
}

func (o Options) mcpApprovalPolicy() MCPApprovalPolicy {
	policy := strings.TrimSpace(string(o.MCPApprovalPolicy))
	if policy == "" {
		return defaultMCPApprovalPolicy
	}
	return MCPApprovalPolicy(policy)
}

func (o *Options) SetReasoningStreaming(enabled bool) {
	if o == nil {
		return
	}
	o.ReasoningStreaming = enabled
	o.reasoningStreamingConfigured = true
}

func (o Options) reasoningStreamingEnabled() bool {
	if !o.reasoningStreamingConfigured {
		return true
	}
	return o.ReasoningStreaming
}

func (o Options) reasoningThoughtsMode() string {
	mode := strings.TrimSpace(o.ReasoningThoughts)
	if mode == "" {
		return defaultReasoningThoughts
	}
	return mode
}

func (o Options) reasoningThoughtsEnabled() bool {
	return o.reasoningThoughtsMode() != reasoningThoughtsOff
}

func (o Options) reasoningThoughtsIncludeSummary() bool {
	switch o.reasoningThoughtsMode() {
	case reasoningThoughtsSummary, reasoningThoughtsBoth:
		return true
	default:
		return false
	}
}

func (o Options) reasoningThoughtsIncludeContent() bool {
	switch o.reasoningThoughtsMode() {
	case reasoningThoughtsContent, reasoningThoughtsBoth:
		return true
	default:
		return false
	}
}

func (o Options) reasoningSummaryMode() string {
	mode := strings.TrimSpace(o.ReasoningSummary)
	if mode == "" {
		return defaultReasoningSummary
	}
	return mode
}

func (o Options) validate() error {
	if err := validateEnumValue("reasoning thoughts", o.reasoningThoughtsMode(), validReasoningThoughtModes); err != nil {
		return err
	}
	if err := validateEnumValue("reasoning summary", o.reasoningSummaryMode(), validReasoningSummaries); err != nil {
		return err
	}
	if err := validateEnumValue("Codex sandbox", o.Sandbox, validCodexSandboxModes); err != nil {
		return err
	}
	if policy := o.mcpApprovalPolicy(); policy != "" {
		if _, ok := validMCPApprovalPolicies[string(policy)]; !ok {
			return fmt.Errorf("invalid value %q for --mcp-approval-policy:\nexpected ask, allow, or deny", policy)
		}
	}
	return nil
}

func validateEnumValue(label string, value string, allowed map[string]struct{}) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if _, ok := allowed[trimmed]; ok {
		return nil
	}
	return fmt.Errorf("invalid %s %q", label, trimmed)
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
