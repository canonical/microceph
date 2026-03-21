package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdCertificateSetRGW struct {
	common             *CmdControl
	flagSSLCertificate string
	flagSSLPrivateKey  string
	flagTarget         string
	flagRestart        bool
}

func (c *cmdCertificateSetRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw --ssl-certificate <base64> --ssl-private-key <base64> [--target <server>] [--restart]",
		Short: "Set the SSL certificate for the RGW service",
		Long: `Set or rotate SSL certificates for the RGW service.

The new certificate and key are written to disk. Use --restart to restart
the RGW service and pick up the new certificate immediately. Without
--restart, the certificate is stored but the service must be restarted
manually for the change to take effect.`,
		RunE: c.Run,
	}

	cmd.Flags().StringVar(&c.flagSSLCertificate, "ssl-certificate", "", "base64 encoded SSL certificate")
	cmd.Flags().StringVar(&c.flagSSLPrivateKey, "ssl-private-key", "", "base64 encoded SSL private key")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.flagRestart, "restart", false, "Restart the RGW service for immediate certificate pickup")

	cmd.MarkFlagRequired("ssl-certificate")
	cmd.MarkFlagRequired("ssl-private-key")

	return cmd
}

func (c *cmdCertificateSetRGW) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := types.CertificateSetRequest{
		SSLCertificate: c.flagSSLCertificate,
		SSLPrivateKey:  c.flagSSLPrivateKey,
		Restart:        c.flagRestart,
	}

	err = client.SetRGWCertificate(context.Background(), cli, req, c.flagTarget)
	if err != nil {
		return err
	}

	if !c.flagRestart {
		fmt.Println("Warning: certificate has been written to disk but is not yet active.")
		fmt.Println("Restart the RGW service with --restart or manually to pick up the new certificate.")
	}

	return nil
}
