package main

import (
	"context"
	"sort"

	"github.com/canonical/lxd/shared"
	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microcluster/microcluster"
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
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
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
		fingerprint, err := shared.CertFingerprintStr(clusterMember.Certificate.String())
		if err != nil {
			continue
		}

		data[i] = []string{clusterMember.Name, clusterMember.Address.String(), clusterMember.Role, fingerprint, string(clusterMember.Status)}
	}

	header := []string{"NAME", "ADDRESS", "ROLE", "FINGERPRINT", "STATUS"}
	sort.Sort(lxdCmd.SortColumnsNaturally(data))

	return lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, clusterMembers)
}
