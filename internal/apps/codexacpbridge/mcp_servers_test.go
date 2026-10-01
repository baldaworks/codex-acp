package codexacp

import (
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestValidateMCPServersRejectsInvalidConfigurations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		servers []acp.McpServer
		wantErr string
	}{
		{
			name:    "missing transport",
			servers: []acp.McpServer{{}},
			wantErr: "mcp server must specify exactly one transport",
		},
		{
			name: "multiple transports named",
			servers: []acp.McpServer{
				{
					Stdio: &acp.McpServerStdio{Name: "docs", Command: "docs"},
					Http:  &acp.McpServerHttpInline{Name: "docs", Url: "http://localhost"},
				},
			},
			wantErr: `mcp server "docs": exactly one transport is required`,
		},
		{
			name: "multiple transports unnamed",
			servers: []acp.McpServer{
				{
					Stdio: &acp.McpServerStdio{Command: "docs"},
					Http:  &acp.McpServerHttpInline{Url: "http://localhost"},
				},
			},
			wantErr: `mcp server "<unnamed>": exactly one transport is required`,
		},
		{
			name: "sse not supported",
			servers: []acp.McpServer{
				{
					Sse: &acp.McpServerSseInline{Name: "events", Url: "http://localhost/sse"},
				},
			},
			wantErr: `transport 'sse' is not supported`,
		},
		{
			name: "duplicate name",
			servers: []acp.McpServer{
				{
					Stdio: &acp.McpServerStdio{Name: "docs", Command: "docs"},
				},
				{
					Http: &acp.McpServerHttpInline{Name: "docs", Url: "http://localhost"},
				},
			},
			wantErr: `mcp server with name "docs" is duplicated`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateMCPServers(tc.servers)
			if err == nil {
				t.Fatalf("validateMCPServers() error = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateMCPServers() error = %q, want to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateMCPServersAcceptsStdioAndHTTP(t *testing.T) {
	got, err := validateMCPServers([]acp.McpServer{
		{
			Stdio: &acp.McpServerStdio{
				Name:    "docs",
				Command: "docs-server",
				Args:    []string{"--serve"},
			},
		},
		{
			Http: &acp.McpServerHttpInline{
				Name: "api",
				Url:  "https://example.test/mcp",
			},
		},
	})
	if err != nil {
		t.Fatalf("validateMCPServers() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("validateMCPServers() entries = %d, want 2", len(got))
	}
	if _, ok := got["docs"]; !ok {
		t.Fatal(`validateMCPServers() missing "docs"`)
	}
	if _, ok := got["api"]; !ok {
		t.Fatal(`validateMCPServers() missing "api"`)
	}
}

func TestFlattenEnvVars(t *testing.T) {
	if got := flattenEnvVars(nil); got != nil {
		t.Fatalf("flattenEnvVars(nil) = %#v, want nil", got)
	}

	got := flattenEnvVars([]acp.EnvVariable{
		{Name: "A", Value: "1"},
		{Name: "A", Value: "2"},
		{Name: "B", Value: "3"},
	})
	if got["A"] != "2" {
		t.Fatalf(`flattenEnvVars()["A"] = %q, want %q`, got["A"], "2")
	}
	if got["B"] != "3" {
		t.Fatalf(`flattenEnvVars()["B"] = %q, want %q`, got["B"], "3")
	}
}

func TestFlattenHTTPHeaders(t *testing.T) {
	if got := flattenHTTPHeaders(nil); got != nil {
		t.Fatalf("flattenHTTPHeaders(nil) = %#v, want nil", got)
	}

	got := flattenHTTPHeaders([]acp.HttpHeader{
		{Name: "Authorization", Value: "Bearer old"},
		{Name: "Authorization", Value: "Bearer new"},
		{Name: "X-Trace", Value: "abc"},
	})
	if got["Authorization"] != "Bearer new" {
		t.Fatalf(`flattenHTTPHeaders()["Authorization"] = %q, want %q`, got["Authorization"], "Bearer new")
	}
	if got["X-Trace"] != "abc" {
		t.Fatalf(`flattenHTTPHeaders()["X-Trace"] = %q, want %q`, got["X-Trace"], "abc")
	}
}
