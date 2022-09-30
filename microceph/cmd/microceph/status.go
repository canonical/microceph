package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

type cmdStatus struct {
	common *CmdControl
}

func (c *cmdStatus) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Checks the cluster status",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdStatus) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Get configured disks.
	disks, err := client.GetDisks(context.Background(), cli)
	if err != nil {
		return err
	}

	// Get services.
	services, err := client.GetServices(context.Background(), cli)
	if err != nil {
		return err
	}

	// Get cluster members.
	clusterMembers, err := cli.GetClusterMembers(context.Background())
	if err != nil {
		return err
	}

	fmt.Println("MicroCeph deployment summary:")

	for _, server := range clusterMembers {
		// Disks.
		diskCount := 0
		for _, disk := range disks {
			if disk.Location != server.Name {
				continue
			}

			diskCount++
		}

		// Services.
		srvServices := []string{}
		for _, service := range services {
			if service.Location != server.Name {
				continue
			}

			srvServices = append(srvServices, service.Service)
		}
		sort.Strings(srvServices)

		if diskCount > 0 {
			srvServices = append(srvServices, "osd")
		}

		fmt.Printf("- %s (%s)\n", server.Name, server.Address.Addr().String())
		fmt.Printf("  Services: %s\n", strings.Join(srvServices, ", "))
		fmt.Printf("  Disks: %d\n", diskCount)
	}

	return nil
}
