package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationConfigureRbd struct {
	common   *CmdControl
	schedule string
}

func (c *cmdRemoteReplicationConfigureRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure <resource>",
		Short: "Configure remote replication parameters for RBD resource (Pool or Image)",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.schedule, "schedule", "", "snapshot schedule in days, hours, or minutes using d, h, m suffix respectively")
	return cmd
}

func (c *cmdRemoteReplicationConfigureRbd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	payload, err := c.prepareRbdPayload(types.ConfigureReplicationRequest, args)
	if err != nil {
		return err
	}

	_, err = client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdRemoteReplicationConfigureRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	pool, image, err := getPoolAndImageFromResource(args[0])
	if err != nil {
		return types.RbdReplicationRequest{}, err
	}

	retReq := types.RbdReplicationRequest{
		SourcePool:   pool,
		SourceImage:  image,
		Schedule:     c.schedule,
		RequestType:  requestType,
		ResourceType: getRbdResourceType(pool, image),
	}

	return retReq, nil
}

// getRbdResourceType gets the resource type of the said request
func getRbdResourceType(poolName string, imageName string) types.RbdResourceType {
	if len(poolName) != 0 && len(imageName) != 0 {
		return types.RbdResourceImage
	} else {
		return types.RbdResourcePool
	}
}

func getPoolAndImageFromResource(resource string) (string, string, error) {
	var pool string
	var image string
	resourceFrags := strings.Split(resource, "/")
	if len(resourceFrags) < 1 || len(resourceFrags) > 2 {
		return "", "", fmt.Errorf("check resource name %s, should be in $pool/$image format", resource)
	}

	// If only pool name is provided.
	if len(resourceFrags) == 1 {
		pool = resourceFrags[0]
		image = ""
	} else
	// if both pool and image names are provided.
	if len(resourceFrags) == 2 {
		pool = resourceFrags[0]
		image = resourceFrags[1]
	}

	return pool, image, nil
}
