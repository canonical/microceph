package main

import "github.com/spf13/cobra"

type cmdCertificate struct {
	common *CmdControl
}

func (c *cmdCertificate) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certificate",
		Short: "Manage SSL certificates",
	}

	// certificate set ...
	cmdSet := cmdCertificateSet{common: c.common}
	cmd.AddCommand(cmdSet.Command())

	// Workaround for subcommand usage errors.
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}

type cmdCertificateSet struct {
	common *CmdControl
}

func (c *cmdCertificateSet) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set SSL certificates for services",
	}

	// certificate set rgw ...
	cmdSetRGW := cmdCertificateSetRGW{common: c.common}
	cmd.AddCommand(cmdSetRGW.Command())

	// Workaround for subcommand usage errors.
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
