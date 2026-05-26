package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

var getEncryptionSupportFunc = func(ctx context.Context, stateDir string) (bool, string, error) {
	m, err := microcluster.App(microcluster.Args{StateDir: stateDir})
	if err != nil {
		return false, "", err
	}
	cli, err := m.LocalClient()
	if err != nil {
		return false, "", err
	}
	return client.GetEncryptionSupport(ctx, cli)
}

type cmdDiskEncryptionSupport struct {
	common *CmdControl
	disk   *cmdDisk
}

func (c *cmdDiskEncryptionSupport) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encryption-support",
		Short: "Check if disk encryption is supported. Exits non-zero with reason if not.",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdDiskEncryptionSupport) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	supported, reason, err := getEncryptionSupportFunc(context.Background(), c.common.FlagStateDir)
	if err != nil {
		return err
	}

	if !supported {
		return fmt.Errorf("%s", reason)
	}

	fmt.Println("Encryption supported.")
	return nil
}
