package main

import (
	"context"
	"sort"

	"github.com/canonical/microcluster/microcluster"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/spf13/cobra"
)

type cmdClusterList struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List servers in the cluster",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterList) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	client, err := m.LocalClient()
	if err != nil {
		return err
	}

	clusterMembers, err := client.GetClusterMembers(context.Background())
	if err != nil {
		return err
	}

	data := make([][]string, len(clusterMembers))
	for i, clusterMember := range clusterMembers {
		data[i] = []string{clusterMember.Name, clusterMember.Address.String(), clusterMember.Role, clusterMember.Certificate.String(), string(clusterMember.Status)}
	}

	header := []string{"NAME", "ADDRESS", "ROLE", "CERTIFICATE", "STATUS"}
	sort.Sort(utils.ByName(data))

	return utils.RenderTable(utils.TableFormatTable, header, data, clusterMembers)
}
