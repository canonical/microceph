package main

import (
	"context"
	"encoding/json"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableRGW struct {
	common             *CmdControl
	wait               bool
	flagPort           int
	flagSSLPort        int
	flagSSLCertificate string
	flagSSLPrivateKey  string
	flagTarget         string
}

func (c *cmdEnableRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw [--port <port>] [--ssl-port <port>] [--ssl-certificate <certificate material>] [--ssl-private-key <private key material>] [--target <server>] [--wait <bool>]",
		Short: "Enable the RGW service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	// The flagPort has a default value of 0 for the case where both the SSL certificate and private key are provided.
	cmd.PersistentFlags().IntVar(&c.flagPort, "port", 0, "Service non-SSL port (default: 80 if no SSL certificate and/or private key are provided)")
	cmd.PersistentFlags().IntVar(&c.flagSSLPort, "ssl-port", 443, "Service SSL port (default: 443)")
	cmd.PersistentFlags().StringVar(&c.flagSSLCertificate, "ssl-certificate", "", "base64 encoded SSL certificate")
	cmd.PersistentFlags().StringVar(&c.flagSSLPrivateKey, "ssl-private-key", "", "base64 encoded SSL private key")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for rgw service to be up.")
	return cmd
}

// Run handles the enable rgw command.
func (c *cmdEnableRGW) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	jsp, err := json.Marshal(ceph.RgwServicePlacement{Port: c.flagPort, SSLPort: c.flagSSLPort, SSLCertificate: c.flagSSLCertificate, SSLPrivateKey: c.flagSSLPrivateKey})
	if err != nil {
		return err
	}

	req := &types.EnableService{
		Name:    "rgw",
		Wait:    c.wait,
		Payload: string(jsp[:]),
	}

	err = client.SendServicePlacementReq(context.Background(), cli, req, c.flagTarget)
	if err != nil {
		return err
	}

	return nil
}
