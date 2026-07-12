package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set via ldflags at build time. For `go install`-based installs
// without ldflags, it falls back to the module's VCS-derived build info.
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("turf %s\n", Version)
		},
	}
}
