package codexacp

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestBuildTurnStartParamsImageDataURLFallback(t *testing.T) {
	params, err := buildTurnStartParams("thr-1", []acp.ContentBlock{
		{
			Image: &acp.ContentBlockImage{
				MimeType: "image/png",
				Data:     "QUJDRA==",
			},
		},
	}, "gpt-5.4", testReasoningXHigh, reasoningSummaryDetailed)
	if err != nil {
		t.Fatalf("buildTurnStartParams() error = %v", err)
	}

	if got := stringValue(params, "threadId"); got != "thr-1" {
		t.Fatalf("threadId = %q, want %q", got, "thr-1")
	}
	if got := stringValue(params, "model"); got != "gpt-5.4" {
		t.Fatalf("model = %q, want %q", got, "gpt-5.4")
	}
	if got := stringValue(params, "effort"); got != testReasoningXHigh {
		t.Fatalf("effort = %q, want %q", got, testReasoningXHigh)
	}
	if got := stringValue(params, "summary"); got != reasoningSummaryDetailed {
		t.Fatalf("summary = %q, want %q", got, reasoningSummaryDetailed)
	}
	input := listValue(params, "input")
	if len(input) != 1 {
		t.Fatalf("input items = %d, want 1", len(input))
	}
	item, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("input[0] type = %T, want map[string]any", input[0])
	}
	if got := stringValue(item, "url"); got != "data:image/png;base64,QUJDRA==" {
		t.Fatalf("input[0].url = %q, want data URL", got)
	}
}

func TestBuildTurnStartParamsSupportsResourceLink(t *testing.T) {
	mimeType := "application/json"
	resource := acp.ResourceLinkBlock("swagger.json", "file:///state/attachments/swagger%20spec.json")
	resource.ResourceLink.MimeType = &mimeType
	params, err := buildTurnStartParams("thr-1", []acp.ContentBlock{
		resource,
	}, "", "", "")
	if err != nil {
		t.Fatalf("buildTurnStartParams() error = %v", err)
	}
	input := listValue(params, "input")
	if len(input) != 1 {
		t.Fatalf("input items = %d, want 1", len(input))
	}
	item, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("input[0] type = %T, want map[string]any", input[0])
	}
	if got := stringValue(item, "type"); got != testInputTypeText {
		t.Fatalf("input[0].type = %q, want text", got)
	}
	want := `Attached resource: name="swagger.json", local_path="/state/attachments/swagger spec.json", mime_type="application/json"`
	if got := stringValue(item, "text"); got != want {
		t.Fatalf("input[0].text = %q, want %q", got, want)
	}
}

func TestBuildThreadResumeParamsIncludesThreadAndOverrides(t *testing.T) {
	params := buildThreadResumeParams(
		"thr-9",
		"/tmp/work",
		codexAppConfig{
			ApprovalPolicy:    testApprovalOnRequest,
			ApprovalsReviewer: testApprovalsReviewerGuard,
			ModelProvider:     "openai",
			Personality:       testPersonalityPragmatic,
			Sandbox:           "workspace-write",
			ServiceTier:       testServiceTierFlex,
		},
		testModelGPT54,
		nil,
	)

	if got := stringValue(params, "threadId"); got != "thr-9" {
		t.Fatalf("threadId = %q, want %q", got, "thr-9")
	}
	if got := stringValue(params, "cwd"); got != "/tmp/work" {
		t.Fatalf("cwd = %q, want %q", got, "/tmp/work")
	}
	if got, ok := boolValue(params, "excludeTurns"); !ok || !got {
		t.Fatalf("excludeTurns = %t (ok=%t), want true", got, ok)
	}
	if got := stringValue(params, "model"); got != testModelGPT54 {
		t.Fatalf("model = %q, want %q", got, testModelGPT54)
	}
}

func TestBuildThreadSettingsUpdateParams(t *testing.T) {
	params := buildThreadSettingsUpdateParams("thr-9", testModelGPT54, testReasoningXHigh, reasoningSummaryDetailed)
	if got := stringValue(params, "threadId"); got != "thr-9" {
		t.Fatalf("threadId = %q, want %q", got, "thr-9")
	}
	if got := stringValue(params, "model"); got != testModelGPT54 {
		t.Fatalf("model = %q, want %q", got, testModelGPT54)
	}
	if got := stringValue(params, "effort"); got != testReasoningXHigh {
		t.Fatalf("effort = %q, want %q", got, testReasoningXHigh)
	}
	if got := stringValue(params, "summary"); got != reasoningSummaryDetailed {
		t.Fatalf("summary = %q, want %q", got, reasoningSummaryDetailed)
	}
}

func TestBuildThreadListParams(t *testing.T) {
	cursor := " cursor-1 "
	cwd := " /tmp/work "
	params := buildThreadListParams(&cursor, &cwd)

	if got := stringValue(params, "cursor"); got != "cursor-1" {
		t.Fatalf("cursor = %q, want %q", got, "cursor-1")
	}
	if got := stringValue(params, "cwd"); got != "/tmp/work" {
		t.Fatalf("cwd = %q, want %q", got, "/tmp/work")
	}
	if got, ok := boolValue(params, "archived"); !ok || got {
		t.Fatalf("archived = %t (ok=%t), want false", got, ok)
	}
	if got := stringValue(params, "sortKey"); got != "updated_at" {
		t.Fatalf("sortKey = %q, want updated_at", got)
	}
	if got := stringValue(params, "sortDirection"); got != "desc" {
		t.Fatalf("sortDirection = %q, want desc", got)
	}
	sourceKinds, ok := params["sourceKinds"].([]string)
	if !ok {
		t.Fatalf("sourceKinds type = %T, want []string", params["sourceKinds"])
	}
	if len(sourceKinds) != len(appServerThreadListSourceKinds) {
		t.Fatalf("sourceKinds len = %d, want %d", len(sourceKinds), len(appServerThreadListSourceKinds))
	}
	for idx, want := range appServerThreadListSourceKinds {
		if got := sourceKinds[idx]; got != want {
			t.Fatalf("sourceKinds[%d] = %q, want %q", idx, got, want)
		}
	}
}
