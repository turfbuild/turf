package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// newConsoleCmd is `turf console [url]`: it signs this browser in to the local
// Turf console, once. Only its owner can open the console; the links an agent
// hands over never carry its token, so a browser signs in with this and keeps
// a cookie. The local engine's server does the work (turf-mcp-server engine
// console), since it is what keeps the token.
func newConsoleCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "console [url]",
		Short: "Sign this browser in to the local Turf console, once",
		Long: "Open the local Turf console in your browser, signed in. Only you can open it: the\n" +
			"links Turf hands you open directly once this browser has signed in. Give a console\n" +
			"link to land on its page.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := resolveMCPServer()
			if err != nil {
				return err
			}
			argv := []string{"engine", "console"}
			if printOnly {
				argv = append(argv, "--print")
			}
			argv = append(argv, args...)
			// server is turf-mcp-server, found as every turf command finds it
			// (--mcp-server, TURF_MCP_SERVER, PATH); argv is fixed apart from
			// the page, which the server validates.
			c := exec.CommandContext(cmd.Context(), server, argv...) //nolint:gosec // G204: see above
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			// The server says what went wrong on stderr; this only adds that it
			// did not sign in.
			if err := c.Run(); err != nil {
				return fmt.Errorf("could not sign in to the Turf console (%s engine console: %w)", server, err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the sign-in link instead of opening it (for a console reached over ssh -L)")
	return cmd
}
