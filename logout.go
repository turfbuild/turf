package main

import (
	"fmt"
	"io"

	"github.com/opentofu/svchost"
	"github.com/spf13/cobra"
)

// turf logout removes a locally-stored API token, mirroring `tofu logout`.
//
// This file deliberately imports neither disco nor net/http: logout is purely
// local, and that import list is the structural guarantee of it. The token is
// only forgotten here, never revoked on the host, so it stays valid until the
// user revokes it there.

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <hostname>",
		Short: "Remove a locally-stored API token for a remote host",
		Long: "Remove the locally-stored API token for a Terraform-compatible host.\n\n" +
			"The token is only removed from local storage, not revoked on the remote server, so it remains valid until you revoke it there.\n\n" +
			"Credentials are read from credentials.tfrc.json in your OpenTofu/Terraform CLI configuration directory. A TF_TOKEN_<hostname> environment variable, and any credentials block in .terraformrc or .tofurc, take precedence over that file and are not affected by this command.",
		Args: exactlyOneHost("logout", "log out of"),
		RunE: func(cmd *cobra.Command, args []string) error {
			host, err := svchost.ForComparison(args[0])
			if err != nil {
				return fmt.Errorf("the given hostname %q is not valid: %w", args[0], err)
			}
			path, err := credentialsFilePath()
			if err != nil {
				return fmt.Errorf("unable to determine credentials file path: %w", err)
			}
			return runLogout(host, path, cmd.OutOrStdout())
		},
	}
}

func runLogout(host svchost.Hostname, credsPath string, out io.Writer) error {
	disp := host.ForDisplay()

	_, stored, err := storedCredential(credsPath, host)
	if err != nil {
		return err
	}
	if !stored {
		// Not an error: the desired end state already holds.
		outf(out, "No credentials for %s are stored.\n", disp)
		return nil
	}

	outf(out, "Removing the stored credentials for %s from the following file:\n    %s\n", disp, credsPath)
	if err := forgetCredential(credsPath, host); err != nil {
		return err
	}

	if envVar, ok := envTokenVarForHost(host); ok {
		outf(out, "\nNote: the environment variable %s is still set for\n%s and will continue to authenticate requests to it.\n", envVar, disp)
	}

	outf(out, "\nSuccess! turf has removed the stored API token for %s.\n", disp)
	return nil
}
