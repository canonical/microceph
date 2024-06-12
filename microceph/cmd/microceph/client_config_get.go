package main

import (
	"context"
	"fmt"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClientConfigGet struct {
	common       *CmdControl
	client       *cmdClient
	clientConfig *cmdClientConfig

	flagHost string
}

func (c *cmdClientConfigGet) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Fetches specified Ceph Client config",
		RunE:  c.Run,
	}

	// * stands for global configs, hence all configs are global by default unless specified.
	cmd.Flags().StringVar(&c.flagHost, "target", "*", "Specify a microceph node the provided config should be applied to.")
	return cmd
}

func (c *cmdClientConfigGet) Run(cmd *cobra.Command, args []string) error {
	allowList := ceph.GetClientConfigSet()

	// Get can be called with a single key.
	if len(args) != 1 {
		return cmd.Help()
	}

	_, ok := allowList[args[0]]
	if !ok {
		return fmt.Errorf("key %s is invalid. \nSupported Keys: %v", args[0], allowList.Keys())
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.ClientConfig{
		Key:  args[0],
		Host: c.flagHost,
	}

	configs, err := client.GetClientConfig(context.Background(), cli, req)
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
