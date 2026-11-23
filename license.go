package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/turfbuild/turf/release"
)

// licenseRule separates the documents `turf license` prints.
const licenseRule = "================================================================================"

func newLicenseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Print the license terms and third-party notices, or accept the terms",
		Long: "Print the Turf Community Edition License 1.0 (LICENSE), the Common Paper Software License " +
			"Agreement Standard Terms 1.1 it incorporates (STANDARD-TERMS.md), and the NOTICE file of trademark " +
			"lines and third-party attributions, as shipped in every release archive (" + termsURL + "). " +
			"`turf license accept` accepts the terms; `turf license status` shows whether they are accepted. " +
			"The turf CLI's own source is open source under MPL-2.0; the release archive as a whole is under the terms printed here.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(),
				release.License+"\n"+licenseRule+"\n\n"+
					release.StandardTerms+"\n"+licenseRule+"\n\n"+
					release.Notice)
			return err
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "accept",
		Short: "Accept the Turf license terms and record the acceptance",
		Long: "Accept the Turf Community Edition License 1.0 (" + termsURL + ") and record it in " +
			"$TURF_HOME/" + markerFileName + " (~/.turf by default). Running this command is the acceptance: " +
			"it asks nothing further. Every turf command, turf-mcp-server and the Homebrew engine service read the record.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := markerPath()
			rec, err := writeMarker(path, methodCommand, time.Now())
			if err != nil {
				return fmt.Errorf("writing %s: %w", path, err)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, acceptedMessage(rec, path)+" A waiting engine service starts within a few seconds.")
			if os.Getenv("TURF_HOME") != "" {
				_, _ = fmt.Fprintf(out, "TURF_HOME is set in this shell; the Homebrew service reads ~/.turf unless TURF_HOME is in its turf.env too.\n")
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether the Turf license terms are accepted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			envVal, envSet := os.LookupEnv(acceptEnv)
			licenseStatus(cmd.OutOrStdout(), resolveAcceptance(flagAcceptEULA, envVal, envSet, markerPath()))
			return nil
		},
	})
	return cmd
}

// licenseStatus prints the acceptance state. It reports, never fails: the
// exit status is 0 accepted or not.
func licenseStatus(out io.Writer, a acceptance) {
	if a.accepted {
		_, _ = fmt.Fprintf(out, "Terms:    accepted\n")
		_, _ = fmt.Fprintf(out, "Version:  %s %s (%s)\n", termsTitle, a.termsVersion, termsURL)
		_, _ = fmt.Fprintf(out, "Method:   %s\n", a.method)
		if !a.acceptedAt.IsZero() {
			_, _ = fmt.Fprintf(out, "Accepted: %s\n", a.acceptedAt.UTC().Format(time.RFC3339))
		}
		switch a.method {
		case methodEnv:
			_, _ = fmt.Fprintf(out, "From:     %s in this environment\n", acceptEnv)
		case methodFlag:
			_, _ = fmt.Fprintf(out, "From:     --%s\n", acceptFlag)
		default:
			_, _ = fmt.Fprintf(out, "From:     %s\n", a.source)
		}
		if (a.method == methodEnv || a.method == methodFlag) && a.marker == nil {
			_, _ = fmt.Fprintf(out, "Note:     this reaches only processes started from here. The Homebrew engine service reads %s: "+
				"turf license accept records it there.\n", a.markerPath)
		}
		return
	}
	_, _ = fmt.Fprintf(out, "Terms:    not accepted\n")
	switch {
	case a.markerMissing:
		_, _ = fmt.Fprintf(out, "Why:      no acceptance recorded in %s\n", a.markerPath)
	case a.markerProblem != "":
		_, _ = fmt.Fprintf(out, "Why:      %s is not a valid acceptance record: %s\n", a.markerPath, a.markerProblem)
	}
	if a.envProblem != "" {
		_, _ = fmt.Fprintf(out, "          %s\n", a.envProblem)
	}
	_, _ = fmt.Fprintf(out, "License:  %s %s, %s\n", termsTitle, termsVersion, termsURL)
	_, _ = fmt.Fprintf(out, "Accept:   turf license accept, or set %s=yes\n", acceptEnv)
}
