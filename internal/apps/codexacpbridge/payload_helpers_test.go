package codexacp

import (
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestBuildTurnInputItemsTrimsTextAndSupportsImageURI(t *testing.T) {
	imageURI := "  https://example.test/image.png  "
	items, err := buildTurnInputItems([]acp.ContentBlock{
		acp.TextBlock("  hello world  "),
		acp.TextBlock("   "),
		{Image: &acp.ContentBlockImage{Uri: &imageURI}},
	})
	if err != nil {
		t.Fatalf("buildTurnInputItems() error = %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("buildTurnInputItems() item count = %d, want 2", len(items))
	}

	textItem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("items[0] type = %T, want map[string]any", items[0])
	}
	if got, want := textItem["type"], testInputTypeText; got != want {
		t.Fatalf("items[0].type = %#v, want %q", got, want)
	}
	if got, want := textItem["text"], "hello world"; got != want {
		t.Fatalf("items[0].text = %#v, want %q", got, want)
	}

	imageItem, ok := items[1].(map[string]any)
	if !ok {
		t.Fatalf("items[1] type = %T, want map[string]any", items[1])
	}
	if got, want := imageItem["url"], "https://example.test/image.png"; got != want {
		t.Fatalf("items[1].url = %#v, want %q", got, want)
	}
}

func TestBuildTurnInputItemsRejectsUnsupportedTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		block   acp.ContentBlock
		wantErr string
	}{
		{
			name:    "audio",
			block:   acp.AudioBlock("Zm9v", "audio/wav"),
			wantErr: "unsupported prompt content block type: audio",
		},
		{
			name:    "resource",
			block:   acp.ContentBlock{Resource: &acp.ContentBlockResource{}},
			wantErr: "unsupported prompt content block type: resource",
		},
		{
			name:    "unknown",
			block:   acp.ContentBlock{},
			wantErr: "unsupported prompt content block type: unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := buildTurnInputItems([]acp.ContentBlock{tc.block})
			if err == nil {
				t.Fatalf("buildTurnInputItems() error = nil, want %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("buildTurnInputItems() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestBuildTurnStartParamsRejectsPromptWithoutSupportedContent(t *testing.T) {
	_, err := buildTurnStartParams("thread-1", []acp.ContentBlock{
		acp.TextBlock("   "),
	}, "", "", "")
	if err == nil {
		t.Fatal("buildTurnStartParams() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "prompt must include at least one text, image, or resource link content block") {
		t.Fatalf("buildTurnStartParams() error = %q, want missing supported-content message", err.Error())
	}
}

func TestResourceLinkText(t *testing.T) {
	t.Parallel()

	mimeType := " application/json "
	cases := []struct {
		name    string
		block   *acp.ContentBlockResourceLink
		want    string
		wantErr string
	}{
		{
			name: "local file URI",
			block: &acp.ContentBlockResourceLink{
				Name:     " swagger.json ",
				Uri:      "file:///state/attachments/swagger%20spec.json",
				MimeType: &mimeType,
			},
			want: `Attached resource: name="swagger.json", local_path="/state/attachments/swagger spec.json", mime_type="application/json"`,
		},
		{
			name:  "non-file URI",
			block: &acp.ContentBlockResourceLink{Name: "docs", Uri: "https://example.test/docs"},
			want:  `Attached resource: name="docs", uri="https://example.test/docs"`,
		},
		{name: "nil", wantErr: "resource link block is required"},
		{name: "missing name", block: &acp.ContentBlockResourceLink{Uri: "file:///tmp/a"}, wantErr: "resource link name is required"},
		{name: "missing URI", block: &acp.ContentBlockResourceLink{Name: "a"}, wantErr: "resource link URI is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resourceLinkText(tc.block)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("resourceLinkText() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resourceLinkText() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("resourceLinkText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageBlockURL(t *testing.T) {
	t.Parallel()

	uri := " https://example.test/image.jpg "
	cases := []struct {
		name    string
		block   *acp.ContentBlockImage
		want    string
		wantErr string
	}{
		{
			name:    "nil block",
			block:   nil,
			wantErr: "image block is required",
		},
		{
			name: "uri preferred",
			block: &acp.ContentBlockImage{
				Uri:      &uri,
				MimeType: "image/png",
				Data:     "QUJDRA==",
			},
			want: "https://example.test/image.jpg",
		},
		{
			name: "data url fallback",
			block: &acp.ContentBlockImage{
				MimeType: "image/png",
				Data:     "QUJDRA==",
			},
			want: "data:image/png;base64,QUJDRA==",
		},
		{
			name: "missing data",
			block: &acp.ContentBlockImage{
				MimeType: "image/png",
			},
			wantErr: "image content block must include uri or mimeType+data",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := imageBlockURL(tc.block)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("imageBlockURL() error = nil, want %q", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("imageBlockURL() error = %q, want %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("imageBlockURL() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("imageBlockURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodexMCPServersConfig(t *testing.T) {
	if got := codexMCPServersConfig(nil); got != nil {
		t.Fatalf("codexMCPServersConfig(nil) = %#v, want nil", got)
	}

	got := codexMCPServersConfig(map[string]acp.McpServer{
		"docs": {
			Stdio: &acp.McpServerStdio{
				Command: "docs-server",
				Args:    []string{"--listen"},
				Env: []acp.EnvVariable{
					{Name: "TOKEN", Value: "abc"},
				},
			},
		},
		"api": {
			Http: &acp.McpServerHttpInline{
				Url: "https://example.test/mcp",
				Headers: []acp.HttpHeader{
					{Name: "Authorization", Value: "Bearer abc"},
				},
			},
		},
		"ignored": {},
	})

	if len(got) != 2 {
		t.Fatalf("codexMCPServersConfig() entries = %d, want 2", len(got))
	}

	docsCfg, ok := got["docs"].(map[string]any)
	if !ok {
		t.Fatalf(`codexMCPServersConfig()["docs"] type = %T, want map[string]any`, got["docs"])
	}
	if docsCfg["command"] != "docs-server" {
		t.Fatalf(`docs.command = %#v, want %q`, docsCfg["command"], "docs-server")
	}
	env, ok := docsCfg["env"].(map[string]string)
	if !ok {
		t.Fatalf(`docs.env type = %T, want map[string]string`, docsCfg["env"])
	}
	if env["TOKEN"] != "abc" {
		t.Fatalf(`docs.env["TOKEN"] = %q, want %q`, env["TOKEN"], "abc")
	}

	apiCfg, ok := got["api"].(map[string]any)
	if !ok {
		t.Fatalf(`codexMCPServersConfig()["api"] type = %T, want map[string]any`, got["api"])
	}
	if apiCfg["url"] != "https://example.test/mcp" {
		t.Fatalf(`api.url = %#v, want %q`, apiCfg["url"], "https://example.test/mcp")
	}
	headers, ok := apiCfg["http_headers"].(map[string]string)
	if !ok {
		t.Fatalf(`api.http_headers type = %T, want map[string]string`, apiCfg["http_headers"])
	}
	if headers["Authorization"] != "Bearer abc" {
		t.Fatalf(`api.http_headers["Authorization"] = %q, want %q`, headers["Authorization"], "Bearer abc")
	}
}

func TestToAppServerToolKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  acp.ToolKind
	}{
		{input: "commandExecution", want: acp.ToolKindExecute},
		{input: "fileChange", want: acp.ToolKindEdit},
		{input: "webSearch", want: acp.ToolKindFetch},
		{input: "mcpToolCall", want: acp.ToolKindExecute},
		{input: "dynamicToolCall", want: acp.ToolKindExecute},
		{input: "imageView", want: acp.ToolKindRead},
		{input: "something-else", want: acp.ToolKindOther},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := toAppServerToolKind(tc.input); got != tc.want {
				t.Fatalf("toAppServerToolKind(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestToACPToolCallStatusAdditionalCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  acp.ToolCallStatus
	}{
		{input: "inProgress", want: acp.ToolCallStatusInProgress},
		{input: "completed", want: acp.ToolCallStatusCompleted},
		{input: "failed", want: acp.ToolCallStatusFailed},
		{input: "declined", want: acp.ToolCallStatusFailed},
		{input: "unknown", want: acp.ToolCallStatusInProgress},
		{input: "  completed  ", want: acp.ToolCallStatusCompleted},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := toACPToolCallStatus(tc.input); got != tc.want {
				t.Fatalf("toACPToolCallStatus(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestToACPPlanStatusAdditionalCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  acp.PlanEntryStatus
	}{
		{input: "pending", want: acp.PlanEntryStatusPending},
		{input: "inProgress", want: acp.PlanEntryStatusInProgress},
		{input: "completed", want: acp.PlanEntryStatusCompleted},
		{input: "unknown", want: acp.PlanEntryStatusPending},
		{input: "  completed  ", want: acp.PlanEntryStatusCompleted},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := toACPPlanStatus(tc.input); got != tc.want {
				t.Fatalf("toACPPlanStatus(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
