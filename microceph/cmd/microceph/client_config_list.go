package main

import (
	"context"
	"fmt"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdClientConfigList struct {
	common       *CmdControl
	client       *cmdClient
	clientConfig *cmdClientConfig

	flagHost string
}

func (c *cmdClientConfigList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lists all configured Ceph Client configs",
		RunE:  c.Run,
	}

	// * stands for global configs, hence all configs are global by default unless specifies.
	cmd.Flags().StringVar(&c.flagHost, "target", "*", "Specify a microceph node the provided config should be applied to.")
	return cmd
}

func (c *cmdClientConfigList) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.ClientConfig{
		Host: c.flagHost,
	}

	configs, err := client.ListClientConfig(context.Background(), cli, req)
	if err != nil {
		return err
	}

	data := make([][]string, len(configs))
	for i, config := range configs {
		data[i] = []string{fmt.Sprintf("%d", i), config.Key, config.Value, config.Host}
	}

	header := []string{"#", "Key", "Value", "Host"}
	err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, configs)
	if err != nil {
		return err
	}

	return nil
}
