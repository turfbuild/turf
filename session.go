package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker-agent/pkg/session"
	"github.com/spf13/cobra"
)

// addResumeFlags registers the --session / --continue resume flags on cmd,
// bound to the shared package-level vars. It is called for the bare `turf`
// command (which runs runChat) and for the chat and exec subcommands — the
// commands that support resume — but not for up/destroy.
func addResumeFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&flagSession, "session", "", "Resume a previous session by ID or relative offset (e.g. -1 for the most recent)")
	cmd.Flags().BoolVarP(&flagContinue, "continue", "c", false, "Resume the most recent session (shorthand for --session -1)")
}

// resolveTurfSession returns the session a run should use, honoring an optional
// resume reference. It is the resume counterpart to newSession and always
// applies turf's session policy (see applyTurfSessionPolicy) — including on a
// session loaded from the store, where cagent's own resume path would leave
// turf's tool pre-approval unset.
//
// The ref (from --session / --continue) is either a concrete session ID or a
// relative offset ("-1" for the last session, resolved via ResolveSessionID
// against the store's summaries). Behavior mirrors cagent's own run path:
//   - empty ref (or no store): a fresh session.
//   - resolved to an existing session: load it and re-stamp turf's policy.
//   - an explicit (non-relative) ID that does not exist yet: create a new
//     session bound to that ID, so a caller (e.g. a supervising script driving
//     `exec` across turns) can own the ID up front — the first run creates it,
//     later runs resume it.
func resolveTurfSession(ctx context.Context, store session.Store, ref string, autoApprove bool) (*session.Session, error) {
	if ref == "" || store == nil {
		return newSession(autoApprove), nil
	}

	id, err := session.ResolveSessionID(ctx, store, ref)
	if err != nil {
		// A relative ref ("-1" from --continue, "-2", …) against an empty store
		// has nothing to resume. Rather than erroring, start a fresh session:
		// `turf --continue` (or `-c`) in a new/empty directory means "continue if
		// there's history, otherwise begin". An out-of-range offset against a
		// NON-empty store is kept as an error (a likely typo, e.g. -5 of 3).
		if session.IsRelativeSessionRef(ref) && storeIsEmpty(ctx, store) {
			return newSession(autoApprove), nil
		}
		return nil, fmt.Errorf("resolving session %q: %w", ref, err)
	}

	sess, err := store.GetSession(ctx, id)
	switch {
	case err == nil:
		applyTurfSessionPolicy(sess, autoApprove)
		return sess, nil
	case errors.Is(err, session.ErrNotFound) && !session.IsRelativeSessionRef(ref):
		sess := newSession(autoApprove)
		session.WithID(id)(sess)
		return sess, nil
	default:
		return nil, fmt.Errorf("loading session %q: %w", id, err)
	}
}

// storeIsEmpty reports whether the session store holds no sessions. It lets a
// relative resume ref (e.g. --continue) fall back to a fresh session instead of
// failing when there is nothing to resume. A summaries error is treated as
// "not empty" so the original, more informative resolve error is surfaced.
func storeIsEmpty(ctx context.Context, store session.Store) bool {
	summaries, err := store.GetSessionSummaries(ctx)
	return err == nil && len(summaries) == 0
}

// resumeRef folds the --session and --continue flags into a single reference for
// resolveTurfSession, and rejects the contradiction of asking to resume while
// session persistence is disabled. --continue is sugar for the most recent
// session ("-1"); an explicit --session value wins if both are given.
func resumeRef(sessionFlag string, continueFlag bool) (string, error) {
	ref := sessionFlag
	if ref == "" && continueFlag {
		ref = "-1"
	}
	if ref != "" && flagNoSession {
		return "", errors.New("cannot resume a session with --no-session set")
	}
	return ref, nil
}
