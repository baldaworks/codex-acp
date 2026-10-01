package codexacp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

const (
	statusInProgress = "inProgress"
	statusCompleted  = "completed"
	statusFailed     = "failed"

	itemTypeCommandExecution = "commandExecution"
	itemTypeFileChange       = "fileChange"
	itemTypeMCPToolCall      = "mcpToolCall"
	itemTypeDynamicToolCall  = "dynamicToolCall"
)

var appServerThreadListSourceKinds = []string{
	"cli",
	"vscode",
	"exec",
	"appServer",
	"subAgent",
	"subAgentReview",
	"subAgentCompact",
	"subAgentThreadSpawn",
	"subAgentOther",
	"unknown",
}

func buildThreadStartParams(cwd string, cfg codexAppConfig, sessionModel string, sessionMCPServers map[string]acp.McpServer) map[string]any {
	params := buildThreadSessionParamsBase(cwd, cfg, sessionModel, sessionMCPServers)
	params["experimentalRawEvents"] = false
	params["persistExtendedHistory"] = false
	return params
}

func buildThreadResumeParams(
	threadID string,
	cwd string,
	cfg codexAppConfig,
	sessionModel string,
	sessionMCPServers map[string]acp.McpServer,
) map[string]any {
	params := buildThreadSessionParamsBase(cwd, cfg, sessionModel, sessionMCPServers)
	params["threadId"] = strings.TrimSpace(threadID)
	params["excludeTurns"] = true
	return params
}

func buildThreadSettingsUpdateParams(threadID string, model string, reasoningEffort string, reasoningSummary string) map[string]any {
	params := map[string]any{
		"threadId": strings.TrimSpace(threadID),
	}
	if trimmedModel := strings.TrimSpace(model); trimmedModel != "" {
		params["model"] = trimmedModel
	}
	if trimmedReasoningEffort := strings.TrimSpace(reasoningEffort); trimmedReasoningEffort != "" {
		params["effort"] = trimmedReasoningEffort
	}
	if trimmedReasoningSummary := strings.TrimSpace(reasoningSummary); trimmedReasoningSummary != "" {
		params["summary"] = trimmedReasoningSummary
	}
	return params
}

func buildThreadListParams(cursor *string, cwd *string) map[string]any {
	params := map[string]any{
		"archived":      false,
		"sortKey":       "updated_at",
		"sortDirection": "desc",
		"sourceKinds":   append([]string(nil), appServerThreadListSourceKinds...),
	}
	if cursor != nil {
		if trimmedCursor := strings.TrimSpace(*cursor); trimmedCursor != "" {
			params["cursor"] = trimmedCursor
		}
	}
	if cwd != nil {
		if trimmedCWD := strings.TrimSpace(*cwd); trimmedCWD != "" {
			params["cwd"] = trimmedCWD
		}
	}
	return params
}

func buildThreadSessionParamsBase(
	cwd string,
	cfg codexAppConfig,
	sessionModel string,
	sessionMCPServers map[string]acp.McpServer,
) map[string]any {
	params := map[string]any{}
	if trimmedCWD := strings.TrimSpace(cwd); trimmedCWD != "" {
		params["cwd"] = trimmedCWD
	}

	effectiveCfg := cfg.withModel(sessionModel)
	if model := strings.TrimSpace(effectiveCfg.Model); model != "" {
		params["model"] = model
	}
	if modelProvider := strings.TrimSpace(effectiveCfg.ModelProvider); modelProvider != "" {
		params["modelProvider"] = modelProvider
	}
	if approval := strings.TrimSpace(effectiveCfg.ApprovalPolicy); approval != "" {
		params["approvalPolicy"] = approval
	}
	if approvalsReviewer := strings.TrimSpace(effectiveCfg.ApprovalsReviewer); approvalsReviewer != "" {
		params["approvalsReviewer"] = approvalsReviewer
	}
	if sandbox := strings.TrimSpace(effectiveCfg.Sandbox); sandbox != "" {
		params["sandbox"] = sandbox
	}
	if personality := strings.TrimSpace(effectiveCfg.Personality); personality != "" {
		params["personality"] = personality
	}
	if serviceTier := strings.TrimSpace(effectiveCfg.ServiceTier); serviceTier != "" {
		params["serviceTier"] = serviceTier
	}
	if effectiveCfg.Ephemeral != nil {
		params["ephemeral"] = *effectiveCfg.Ephemeral
	}
	if baseInstructions := strings.TrimSpace(effectiveCfg.BaseInstructions); baseInstructions != "" {
		params["baseInstructions"] = baseInstructions
	}
	if developerInstructions := strings.TrimSpace(effectiveCfg.DeveloperInstructions); developerInstructions != "" {
		params["developerInstructions"] = developerInstructions
	}

	config := cloneMap(effectiveCfg.Config)
	if config == nil {
		config = map[string]any{}
	}
	if profile := strings.TrimSpace(effectiveCfg.Profile); profile != "" {
		config["profile"] = profile
	}
	if compactPrompt := strings.TrimSpace(effectiveCfg.CompactPrompt); compactPrompt != "" {
		config["compact_prompt"] = compactPrompt
	}
	if mcpServersCfg := codexMCPServersConfig(sessionMCPServers); len(mcpServersCfg) > 0 {
		config["mcp_servers"] = mcpServersCfg
	}
	if len(config) > 0 {
		params["config"] = config
	}

	return params
}

func buildTurnStartParams(
	threadID string,
	prompt []acp.ContentBlock,
	model string,
	reasoningEffort string,
	reasoningSummary string,
) (map[string]any, error) {
	inputItems, err := buildTurnInputItems(prompt)
	if err != nil {
		return nil, err
	}
	if len(inputItems) == 0 {
		return nil, fmt.Errorf("prompt must include at least one text, image, or resource link content block")
	}

	params := map[string]any{
		"threadId": strings.TrimSpace(threadID),
		"input":    inputItems,
	}
	if trimmedModel := strings.TrimSpace(model); trimmedModel != "" {
		params["model"] = trimmedModel
	}
	if trimmedReasoningEffort := strings.TrimSpace(reasoningEffort); trimmedReasoningEffort != "" {
		params["effort"] = trimmedReasoningEffort
	}
	if trimmedReasoningSummary := strings.TrimSpace(reasoningSummary); trimmedReasoningSummary != "" {
		params["summary"] = trimmedReasoningSummary
	}
	return params, nil
}

func buildTurnInputItems(prompt []acp.ContentBlock) ([]any, error) {
	items := make([]any, 0, len(prompt))
	for _, block := range prompt {
		switch {
		case block.Text != nil:
			trimmed := strings.TrimSpace(block.Text.Text)
			if trimmed == "" {
				continue
			}
			items = append(items, textInputItem(trimmed))
		case block.Image != nil:
			url, err := imageBlockURL(block.Image)
			if err != nil {
				return nil, err
			}
			items = append(items, map[string]any{
				"type": "image",
				"url":  url,
			})
		case block.Audio != nil:
			return nil, fmt.Errorf("unsupported prompt content block type: audio")
		case block.ResourceLink != nil:
			text, err := resourceLinkText(block.ResourceLink)
			if err != nil {
				return nil, err
			}
			items = append(items, textInputItem(text))
		case block.Resource != nil:
			return nil, fmt.Errorf("unsupported prompt content block type: resource")
		default:
			return nil, fmt.Errorf("unsupported prompt content block type: unknown")
		}
	}
	return items, nil
}

func textInputItem(text string) map[string]any {
	return map[string]any{
		"type":          "text",
		"text":          text,
		"text_elements": []any{},
	}
}

func resourceLinkText(block *acp.ContentBlockResourceLink) (string, error) {
	if block == nil {
		return "", fmt.Errorf("resource link block is required")
	}
	name := strings.TrimSpace(block.Name)
	if name == "" {
		return "", fmt.Errorf("resource link name is required")
	}
	uri := strings.TrimSpace(block.Uri)
	if uri == "" {
		return "", fmt.Errorf("resource link URI is required")
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse resource link URI: %w", err)
	}

	fields := []string{"name=" + strconv.Quote(name)}
	if strings.EqualFold(parsed.Scheme, "file") &&
		(parsed.Host == "" || strings.EqualFold(parsed.Host, "localhost")) &&
		parsed.RawQuery == "" && parsed.Fragment == "" && filepath.IsAbs(parsed.Path) {
		fields = append(fields, "local_path="+strconv.Quote(filepath.Clean(filepath.FromSlash(parsed.Path))))
	} else {
		fields = append(fields, "uri="+strconv.Quote(uri))
	}
	if block.MimeType != nil {
		if mimeType := strings.TrimSpace(*block.MimeType); mimeType != "" {
			fields = append(fields, "mime_type="+strconv.Quote(mimeType))
		}
	}
	return "Attached resource: " + strings.Join(fields, ", "), nil
}

func imageBlockURL(block *acp.ContentBlockImage) (string, error) {
	if block == nil {
		return "", fmt.Errorf("image block is required")
	}
	if block.Uri != nil {
		if uri := strings.TrimSpace(*block.Uri); uri != "" {
			return uri, nil
		}
	}
	mimeType := strings.TrimSpace(block.MimeType)
	data := strings.TrimSpace(block.Data)
	if mimeType == "" || data == "" {
		return "", fmt.Errorf("image content block must include uri or mimeType+data")
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, data), nil
}

func codexMCPServersConfig(sessionMCPServers map[string]acp.McpServer) map[string]any {
	if len(sessionMCPServers) == 0 {
		return nil
	}
	result := make(map[string]any, len(sessionMCPServers))
	for name, server := range sessionMCPServers {
		serverCfg := map[string]any{}
		switch {
		case server.Stdio != nil:
			serverCfg["command"] = server.Stdio.Command
			if len(server.Stdio.Args) > 0 {
				serverCfg["args"] = server.Stdio.Args
			}
			if env := flattenEnvVars(server.Stdio.Env); len(env) > 0 {
				serverCfg["env"] = env
			}
		case server.Http != nil:
			serverCfg["url"] = server.Http.Url
			if headers := flattenHTTPHeaders(server.Http.Headers); len(headers) > 0 {
				serverCfg["http_headers"] = headers
			}
		default:
			continue
		}
		if len(serverCfg) > 0 {
			result[name] = serverCfg
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func toAppServerToolKind(itemType string) acp.ToolKind {
	switch itemType {
	case itemTypeCommandExecution:
		return acp.ToolKindExecute
	case itemTypeFileChange:
		return acp.ToolKindEdit
	case "webSearch":
		return acp.ToolKindFetch
	case itemTypeMCPToolCall, itemTypeDynamicToolCall:
		return acp.ToolKindExecute
	case "imageView":
		return acp.ToolKindRead
	default:
		return acp.ToolKindOther
	}
}

func toACPToolCallStatus(status string) acp.ToolCallStatus {
	switch strings.TrimSpace(status) {
	case statusInProgress:
		return acp.ToolCallStatusInProgress
	case statusCompleted:
		return acp.ToolCallStatusCompleted
	case statusFailed, "declined":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusInProgress
	}
}

func toACPPlanStatus(status string) acp.PlanEntryStatus {
	switch strings.TrimSpace(status) {
	case "pending":
		return acp.PlanEntryStatusPending
	case statusInProgress:
		return acp.PlanEntryStatusInProgress
	case statusCompleted:
		return acp.PlanEntryStatusCompleted
	default:
		return acp.PlanEntryStatusPending
	}
}
