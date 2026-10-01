//go:build integration

package codexacp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// BenchmarkBridgeIntegration measures complete ACP calls over stdio. Real Codex
// is opt-in because prompts use the authenticated account and model quota.
func BenchmarkBridgeIntegration(b *testing.B) {
	b.StopTimer()
	cwd := integrationWorkingDir(b)
	bin := buildIntegrationBridgeBinary(b, cwd)
	modes := []string{"Local"}
	if os.Getenv("CODEX_ACP_BENCH_REAL") == "1" {
		modes = append(modes, "Codex")
	}
	for _, mode := range modes {
		b.Run(mode, func(b *testing.B) {
			b.Run("NewSession", func(b *testing.B) {
				client := benchmarkClient(b, cwd, bin, mode)
				warm := benchmarkSession(b, client, cwd)
				benchmarkCloseSession(b, client, warm)
				b.ResetTimer()
				for range b.N {
					id := benchmarkSession(b, client, cwd)
					b.StopTimer()
					benchmarkCloseSession(b, client, id)
					b.StartTimer()
				}
			})
			b.Run("PromptRoundTrip", func(b *testing.B) {
				client := benchmarkClient(b, cwd, bin, mode)
				id := benchmarkSession(b, client, cwd)
				b.ResetTimer()
				for range b.N {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					updates, result, err := client.Prompt(ctx, string(id), "Reply with exactly OK. Do not use tools.")
					if err != nil {
						cancel()
						b.Fatal(err)
					}
					response := awaitIntegrationPromptResult(ctx, updates, result)
					cancel()
					if response.Err != nil || response.Response.StopReason != acp.StopReasonEndTurn {
						b.Fatalf("prompt failed: stop=%s err=%v", response.Response.StopReason, response.Err)
					}
				}
			})
			for _, sessions := range []int{1, 10, 100} {
				b.Run(fmt.Sprintf("Memory/Sessions=%d", sessions), func(b *testing.B) {
					benchmarkSessionMemory(b, cwd, bin, mode, sessions)
				})
			}
		})
	}
}

func benchmarkClient(b *testing.B, cwd, bin, mode string) *integrationACPClient {
	b.Helper()
	if mode == "Local" {
		client, stderr := newHelperBackedACPClient(b, cwd, bin, newFakeCodexHelperHarness(b), "steady")
		helperMustInitialize(b, client, stderr)
		return client
	}
	client, err := newIntegrationACPClient(context.Background(), integrationACPClientConfig{
		Command: []string{bin}, WorkingDir: cwd,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := client.Initialize(ctx); err != nil {
		b.Fatal(err)
	}
	return client
}

func benchmarkSession(b *testing.B, client *integrationACPClient, cwd string) acp.SessionId {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	response, err := client.NewSessionWithMeta(ctx, cwd, nil, map[string]any{
		"codex": map[string]any{"ephemeral": true},
	})
	if err != nil {
		b.Fatal(err)
	}
	if response.SessionId == "" {
		b.Fatal("empty session ID")
	}
	return response.SessionId
}

func benchmarkCloseSession(b *testing.B, client *integrationACPClient, id acp.SessionId) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := client.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: id}); err != nil {
		b.Fatal(err)
	}
}

func benchmarkSessionMemory(b *testing.B, cwd, bin, mode string, sessions int) {
	b.Helper()
	if runtime.GOOS != "linux" {
		b.Skip("process RSS measurement requires Linux /proc")
	}
	b.StopTimer()
	var bridgeTotal, backendTotal, bridgeGrowth, backendGrowth float64
	// Keep elapsed time for Go's adaptive calibration, even though this benchmark
	// reports memory rather than latency. A stopped timer makes default runs grow
	// toward billions of samples. Suppress ns/op below instead.
	b.StartTimer()
	for range b.N {
		client := benchmarkClient(b, cwd, bin, mode)
		warm := benchmarkSession(b, client, cwd)
		benchmarkCloseSession(b, client, warm)
		time.Sleep(100 * time.Millisecond)
		baseBridge, baseBackend := benchmarkRSS(b, client.PID())
		for range sessions {
			_ = benchmarkSession(b, client, cwd)
		}
		time.Sleep(100 * time.Millisecond)
		bridge, backend := benchmarkRSS(b, client.PID())
		bridgeTotal += bridge
		backendTotal += backend
		bridgeGrowth += bridge - baseBridge
		backendGrowth += backend - baseBackend
		if err := client.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	iterations := float64(b.N)
	b.ReportMetric(0, "ns/op")
	b.ReportMetric(bridgeTotal/iterations, "bridge-RSS-B")
	b.ReportMetric(backendTotal/iterations, "backend-RSS-B")
	b.ReportMetric(bridgeGrowth/iterations/float64(sessions), "bridge-growth-B/session")
	b.ReportMetric(backendGrowth/iterations/float64(sessions), "backend-growth-B/session")
}

func benchmarkRSS(b *testing.B, pid int) (float64, float64) {
	b.Helper()
	bridge, err := processRSS(pid)
	if err != nil {
		b.Fatal(err)
	}
	backend, err := descendantRSS(pid)
	if err != nil {
		b.Fatal(err)
	}
	return float64(bridge), float64(backend)
}

func processRSS(pid int) (int64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	return rssFromStatm(string(raw), int64(os.Getpagesize()))
}

func rssFromStatm(raw string, pageSize int64) (int64, error) {
	fields := strings.Fields(raw)
	if len(fields) < 2 || pageSize <= 0 {
		return 0, fmt.Errorf("invalid statm or page size: %q, %d", raw, pageSize)
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || pages < 0 {
		return 0, fmt.Errorf("invalid resident pages: %q", fields[1])
	}
	return pages * pageSize, nil
}

// Children may be spawned by any OS thread; checking only the main task misses
// Go's exec children. RSS totals include each descendant process exactly once.
func descendantRSS(pid int) (int64, error) {
	paths, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
	if err != nil {
		return 0, err
	}
	seen := map[int]bool{}
	var total int64
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		for _, field := range strings.Fields(string(raw)) {
			child, err := strconv.Atoi(field)
			if err != nil {
				return 0, err
			}
			if seen[child] {
				continue
			}
			seen[child] = true
			rss, err := processRSS(child)
			if err != nil {
				return 0, err
			}
			nested, err := descendantRSS(child)
			if err != nil {
				return 0, err
			}
			total += rss + nested
		}
	}
	return total, nil
}

func TestRSSFromStatm(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      int64
	}{
		{name: "resident pages", raw: "100 12 4 0 0", want: 49152},
		{name: "zero", raw: "100 0 4", want: 0},
		{name: "missing", raw: "100", want: -1},
		{name: "malformed", raw: "100 invalid", want: -1},
		{name: "negative", raw: "100 -1", want: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rssFromStatm(tc.raw, 4096)
			if tc.want < 0 {
				if err == nil {
					t.Fatal("invalid input accepted")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("RSS=%d err=%v, want %d", got, err, tc.want)
			}
		})
	}
}
