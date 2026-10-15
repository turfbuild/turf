package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/turfbuild/turf/release"
)

// licenseLines is the short statement printed under the version by
// `turf --version` and `turf version`. It is the compliance one-liner: the
// license, what it asks of a company, and where to get a commercial license.
const licenseLines = "License: PolyForm Noncommercial 1.0.0 — commercial use requires a license\n" +
	"https://turf.build/licensing\n"

func newLicenseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "license",
		Short: "Print the license and third-party notices",
		Long: "Print the Turf license (the PolyForm Noncommercial License 1.0.0 with its Required Notice) " +
			"and the NOTICE file of trademark lines and third-party attributions, as shipped in every release archive. " +
			"The turf CLI's own source is open source under MPL-2.0; the release archive as a whole is the Larger Work under the license printed here.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			out := cmd.OutOrStdout()
			fmt.Fprint(out, release.License)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "================================================================================")
			fmt.Fprintln(out)
			fmt.Fprint(out, release.Notice)
		},
	}
}
