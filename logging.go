package main

import (
	"log/slog"
	"path/filepath"

	"github.com/docker/docker-agent/pkg/logging"
)

const debugLogName = "turf.debug.log"

// setupLogging configures turf's process-wide slog handler. cagent's runtime
// logs through the global slog default, and any line written to stderr while
// the Bubbletea TUI owns the alternate screen paints over the UI — the classic
// symptom is a model error dumping log spew across the chat. So turf never
// logs to stderr; everything goes to a rotating file at
// <TURF_HOME>/turf.debug.log — turf's own home, resolved directly via
// turfHome() because paths.SetDataDir is not redirected to it until the TUI
// starts (which is after this runs).
//
// The file always captures Warn and above: cagent reports some silent-failure
// diagnoses only through slog (e.g. its "Empty assistant turn" warning when a
// model returns an empty response and the turn ends with no output), and
// discarding those made such failures undiagnosable after the fact. With
// --debug (env TURF_DEBUG) the file captures debug level and up instead.
//
// This governs turf's in-process logging only; it is independent of the
// --log-* flags, which pass through to the separate turf-mcp-server
// subprocess.
func setupLogging() error {
	path := filepath.Join(turfHome(), debugLogName)
	f, err := logging.NewRotatingFile(path)
	if err != nil {
		// Never paint stderr and never block launch: fall back to discarding.
		// Surface the failure only when the user explicitly asked for logs.
		slog.SetDefault(slog.New(slog.DiscardHandler))
		if flagDebug {
			return err
		}
		return nil
	}
	level := slog.LevelWarn
	if flagDebug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})))
	return nil
}
