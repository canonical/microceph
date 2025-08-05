package main

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/clilogger"
	"sort"
	"strings"

	"github.com/canonical/microcluster/v2/microcluster"
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
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
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
	clilogger.Debugf("Disks: %+v", disks)

	// Get services.
	services, err := client.GetServices(context.Background(), cli)
	if err != nil {
		return err
	}
	clilogger.Debugf("Services: %+v", services)

	// Get cluster members.
	clusterMembers, err := cli.GetClusterMembers(context.Background())
	if err != nil {
		return err
	}
	clilogger.Debugf("Members: %+v", clusterMembers)
	
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

			// grouped service should appear as service.groupId
			service_name := service.Service
			if len(service.GroupID) != 0 {
				service_name = fmt.Sprintf("%s.%s", service.Service, service.GroupID)
			}

			srvServices = append(srvServices, service_name)
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
