package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"
)

// appName is turf's brand name, shown in the TUI status bar and window title in
// place of cagent's "docker agent" default.
const appName = "turf"

var (
	flagModel          string
	flagModelBaseURL   string
	flagTmpDir         string
	flagPluginCacheDir string
	flagChdir          string
	flagMemoryPath     string
	flagNoMemory       bool
	flagNoTelemetry    bool
	flagMCPServer      string
	flagLogFile        string
	flagLogLevel       string
	flagLogFormat      string
	flagTheme          string
	flagDebug          bool
	flagLean           bool
)

func execute(ctx context.Context, args []string) error {
	rootCmd := newRootCmd()
	rootCmd.SetArgs(args)
	return rootCmd.ExecuteContext(ctx)
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "turf",
		Short:         "A drop-in replacement for Terraform with agentic superpowers",
		Long:          "turf is a drop-in replacement for Terraform with agentic superpowers: full support for Terraform HCL and the module registry, plus an AI agent that plans, applies, and destroys cloud infrastructure across any OpenTofu provider. Describe changes in natural language (chat) or point it at HCL configuration (up, destroy); turf drives the turf MCP server to carry them out.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Running turf with no subcommand launches the interactive chat TUI, so
		// `turf` is equivalent to `turf chat`. Args is left unset so cobra's
		// legacyArgs still reports unknown subcommands (with did-you-mean
		// suggestions) rather than silently opening chat on a typo.
		RunE: runChat,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Configure logging before anything else so turf's own diagnostic
			// logs never paint over the TUI's alternate screen (see logging.go).
			if err := setupLogging(); err != nil {
				return err
			}
			if flagNoTelemetry {
				// cagent's runtime and the turf-mcp-server subprocess both read
				// this env var at startup (pkg/telemetry/utils.go). Setting it
				// here, before any subcommand builds the runtime or launches the
				// subprocess, disables telemetry uniformly for chat/up/destroy.
				// We only ever force-disable: leaving the flag unset preserves
				// whatever TELEMETRY_ENABLED the user set in their environment.
				if err := os.Setenv("TELEMETRY_ENABLED", "false"); err != nil {
					return err
				}
			}
			if flagChdir != "" {
				return os.Chdir(flagChdir)
			}
			return nil
		},
	}

	// --chdir is the primary name; -C is kept as the git/make-style short alias.
	cmd.PersistentFlags().StringVarP(&flagChdir, "chdir", "C", "", "Switch to this directory before running")
	// Default to the stable "latest GA Pro" alias rather than a -preview pin:
	// Google rotates and retires preview models, so a preview default eventually
	// 404s without warning. gemini-pro-latest auto-tracks the current GA Pro.
	cmd.PersistentFlags().StringVar(&flagModel, "model", envOrDefault("TURF_MODEL", "google/gemini-pro-latest"), "LLM model as provider/model, e.g. anthropic/claude-sonnet-4-6 or the keyless local dmr/ai/qwen3 (env: TURF_MODEL)")
	cmd.PersistentFlags().StringVar(&flagModelBaseURL, "base-url", os.Getenv("TURF_MODEL_BASE_URL"), "Override the model API endpoint for OpenAI-compatible servers (vLLM, LM Studio, gateways); DMR/Ollama resolve their own (env: TURF_MODEL_BASE_URL)")
	cmd.PersistentFlags().StringVar(&flagTmpDir, "tmp-dir", os.Getenv("TURF_TMP_DIR"), "Per-run scratch dir for the server (provider target + phase dirs); for the persistent plugin cache use --plugin-cache-dir (env: TURF_TMP_DIR)")
	cmd.PersistentFlags().StringVar(&flagPluginCacheDir, "plugin-cache-dir", os.Getenv("TURF_PLUGIN_CACHE_DIR"), "Shared provider plugin cache, persisted across runs (env: TURF_PLUGIN_CACHE_DIR; default: <user cache>/turf/plugins; 'off' disables)")
	cmd.PersistentFlags().StringVar(&flagMemoryPath, "memory-path", "", "Path to SQLite memory database (default: .turf-memory.db in working dir)")
	cmd.PersistentFlags().BoolVar(&flagNoMemory, "no-memory", false, "Disable persistent agent memory")
	cmd.PersistentFlags().BoolVar(&flagNoTelemetry, "no-telemetry", os.Getenv("TURF_NO_TELEMETRY") != "", "Disable anonymous usage telemetry sent by the underlying agent runtime (env: TURF_NO_TELEMETRY)")
	cmd.PersistentFlags().StringVar(&flagMCPServer, "mcp-server", os.Getenv("TURF_MCP_SERVER"), "Path to turf-mcp-server binary (default: looked up on PATH; env: TURF_MCP_SERVER)")
	cmd.PersistentFlags().StringVar(&flagLogFile, "log-file", "", "Write turf-mcp-server logs to this file (default: stderr; env passthrough: TF_LOG_PATH)")
	cmd.PersistentFlags().StringVar(&flagLogLevel, "log-level", "", "turf-mcp-server log level: trace|debug|info|warn|error|off (env passthrough: TF_LOG_CORE / TF_LOG)")
	cmd.PersistentFlags().StringVar(&flagLogFormat, "log-format", "", "turf-mcp-server log format: text|json (env passthrough: TF_LOG=...-JSON)")
	cmd.PersistentFlags().StringVar(&flagTheme, "theme", os.Getenv("TURF_THEME"), "TUI theme name; overrides the saved /theme choice for this run (default: calm-roots; env: TURF_THEME)")
	cmd.PersistentFlags().BoolVar(&flagDebug, "debug", os.Getenv("TURF_DEBUG") != "", "Log at debug level to <TURF_HOME>/turf.debug.log (default: warnings and errors only; env: TURF_DEBUG)")
	cmd.PersistentFlags().BoolVar(&flagLean, "lean", os.Getenv("TURF_LEAN") != "", "Use the simplified lean TUI (no sidebar/tabs/overlays; renders to scrollback; env: TURF_LEAN)")
	addWorktreeFlags(cmd)

	cmd.AddCommand(newUpCmd())
	cmd.AddCommand(newDestroyCmd())
	cmd.AddCommand(newChatCmd())
	cmd.AddCommand(newVersionCmd())

	return cmd
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
