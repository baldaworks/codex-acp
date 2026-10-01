package cobracmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	codexacpbridge "github.com/baldaworks/codex-acp/internal/apps/codexacpbridge"
	"github.com/baldaworks/codex-acp/internal/logging"
	appversion "github.com/baldaworks/codex-acp/internal/version"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	runProxy    = codexacpbridge.RunProxy
	initLogging = logging.Init
)

const bridgeDefaultAgentName = "codex-acp"

func New() *cobra.Command {
	opts := codexacpbridge.Options{}
	var debugLogs bool
	reasoningStreaming := true
	reasoningThoughts := "summary"
	reasoningSummary := "auto"
	mcpApprovalPolicy := "ask"

	cmd := &cobra.Command{
		Use:          "codex-acp [flags]",
		Short:        "Expose Codex bridge backend as ACP over stdio",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			runOpts := opts
			runOpts.SetReasoningStreaming(reasoningStreaming)
			runOpts.ReasoningThoughts = reasoningThoughts
			runOpts.ReasoningSummary = reasoningSummary
			runOpts.MCPApprovalPolicy = codexacpbridge.MCPApprovalPolicy(mcpApprovalPolicy)
			if strings.TrimSpace(runOpts.Name) == "" {
				runOpts.Name = bridgeDefaultAgentName
			}

			logLevel := logging.LevelInfo
			if debugLogs {
				logLevel = logging.LevelDebug
			}
			if err := initLogging(logging.WithLevel(logLevel)); err != nil {
				return fmt.Errorf("initialize logging: %w", err)
			}
			ctx := log.Logger.With().Str("component", "codex.acp.bridge").Logger().WithContext(cmd.Context())

			return runProxy(ctx, workingDir, runOpts, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "ACP agent name exposed via initialize (defaults to codex-acp)")
	cmd.Flags().BoolVar(&opts.DeferBackend, "defer-backend", false, "defer Codex backend validation until a session operation")
	cmd.Flags().BoolVar(&opts.MessageStreaming, "message-streaming", false, "stream app-server agentMessage deltas as ACP agent_message_chunk updates")
	cmd.Flags().BoolVar(&reasoningStreaming, "reasoning-streaming", true, "stream app-server reasoning deltas as ACP agent_thought_chunk updates")
	cmd.Flags().StringVar(&reasoningThoughts, "reasoning-thoughts", reasoningThoughts, "reasoning thought lane to project: off, summary, content, or both")
	cmd.Flags().StringVar(&reasoningSummary, "reasoning-summary", reasoningSummary, "app-server reasoning summary level to request: auto, concise, detailed, or none")
	cmd.Flags().StringArrayVar(&opts.CodexArgs, "codex-args", nil, "repeatable global argument inserted before the `app-server` subcommand")
	cmd.Flags().StringVar(&mcpApprovalPolicy, "mcp-approval-policy", mcpApprovalPolicy, "MCP tool-call approval policy: ask, allow, or deny")
	cmd.Flags().StringVar(&opts.Sandbox, "sandbox", "", "Codex sandbox mode applied to the CLI and ACP sessions: read-only, workspace-write, or danger-full-access")
	cmd.Flags().BoolVar(&debugLogs, "debug", false, "enable debug logging")
	cmd.Long = "Run the Codex bridge backend and expose it as an ACP agent over stdio. Configure per-session Codex behavior using ACP session/new _meta.codex."
	//nolint:dupword
	cmd.Example = `  codex-acp
  codex-acp --name team-codex
  codex-acp --defer-backend
  codex-acp version
  codex-acp --message-streaming
  codex-acp --reasoning-thoughts=both
  codex-acp --reasoning-summary=detailed
  codex-acp --reasoning-streaming=false
  codex-acp --mcp-approval-policy=allow
  codex-acp --sandbox=danger-full-access
  codex-acp --debug`
	cmd.AddCommand(loginCommand(runCodexLogin), versionCommand())
	return cmd
}

// Command is kept as a compatibility alias for older import sites.
func Command() *cobra.Command {
	return New()
}

type loginRunner func(context.Context, io.Reader, io.Writer, io.Writer) error

func loginCommand(run loginRunner) *cobra.Command {
	return &cobra.Command{
		Use:          "login",
		Short:        "Authenticate with Codex",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := run(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("codex login: %w", err)
			}
			return nil
		},
	}
}

func runCodexLogin(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "codex", "login")
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print the bridge version",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), appversion.String())
			return err
		},
	}
}
