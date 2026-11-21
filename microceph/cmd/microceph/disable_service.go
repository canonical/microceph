package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

// deleteServiceFunc asks the daemon on target to disable service. It is a
// variable so tests can check the request without a running daemon.
var deleteServiceFunc = func(ctx context.Context, stateDir string, target string, service string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: stateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	return client.DeleteService(ctx, cli, target, service)
}

// cmdDisableService disables a service that needs no other input than the
// server it runs on. The daemon does the teardown, including the quorum
// check that refuses to remove a MON the cluster cannot spare.
type cmdDisableService struct {
	common     *CmdControl
	service    string
	label      string // Service name as the help text spells it, e.g. "RBD Mirror".
	flagTarget string
}

func (c *cmdDisableService) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [--target <server>]", c.service),
		Short: fmt.Sprintf("Disable the %s service on the --target server (default: this server)", c.label),
		Args:  cobra.NoArgs,
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the disable command of the service.
func (c *cmdDisableService) Run(cmd *cobra.Command, args []string) error {
	return deleteServiceFunc(context.Background(), c.common.FlagStateDir, c.flagTarget, c.service)
}
