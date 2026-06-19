package main

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
)

type cmdClusterBootstrapCeph struct {
	common  *CmdControl
	cluster *cmdCluster

	flagTarget           string
	flagMonIp            string
	flagPubNet           string
	flagClusterNet       string
	flagV2Only           bool
	flagAvailabilityZone string
	flagForce            bool
}

func (c *cmdClusterBootstrapCeph) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap-ceph",
		Short: "Bootstrap Ceph on an existing MicroCluster member (CE142 role-managed mode)",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagTarget, "target", "", "Target MicroCluster member name for Ceph bootstrap (required)")
	cmd.Flags().StringVar(&c.flagMonIp, "mon-ip", "", "Public address for bootstrapping ceph mon service.")
	cmd.Flags().StringVar(&c.flagPubNet, "public-network", "", "Public network Ceph daemons bind to.")
	cmd.Flags().StringVar(&c.flagClusterNet, "cluster-network", "", "Cluster network Ceph daemons bind to.")
	cmd.Flags().BoolVar(&c.flagV2Only, "v2-only", false, "Whether to support V2 messenger only or both V1 and V2")
	cmd.Flags().StringVar(&c.flagAvailabilityZone, "availability-zone", "", "Availability zone for the bootstrap target host.")
	cmd.Flags().BoolVar(&c.flagForce, "force", false, "Recover from a stale in_progress bootstrap state (reset to failed then retry). Not for normal use. Must not be used while a live bootstrap may be running on another member.")

	return cmd
}

func (c *cmdClusterBootstrapCeph) Run(cmd *cobra.Command, args []string) error {
	if c.flagTarget == "" {
		return fmt.Errorf("--target is required")
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()

	err = m.Ready(ctx)
	if err != nil {
		return fmt.Errorf("fault while waiting for App readiness: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Target the requested member so microcluster proxies the PUT to that
	// member's daemon. The handler runs on the target member where
	// s.Name()==target, so prodCephBootstrapStepsFunc bootstraps Ceph locally
	// on the correct node. This mirrors how SendServicePlacementReq and
	// DeleteService target members via UseTarget.
	cli = cli.UseTarget(c.flagTarget)

	req := c.buildRequest()

	url := api.NewURL().Path("ceph", "bootstrap")
	// Pass the struct pointer directly: cli.Query JSON-encodes non-reader data,
	// so passing pre-marshaled []byte would base64-encode it into a JSON string
	// and the handler could not unmarshal it into CephBootstrapRequest.
	err = cli.Query(ctx, "PUT", types.ExtendedPathPrefix, &url.URL, &req, nil)
	if err != nil {
		return fmt.Errorf("ceph-only bootstrap failed: %w", err)
	}

	fmt.Printf("Ceph bootstrap initiated on member %s\n", c.flagTarget)
	return nil
}

// buildRequest constructs the CephBootstrapRequest from the CLI flags. It is a
// separate method so tests can verify the request payload without a live
// microcluster daemon.
func (c *cmdClusterBootstrapCeph) buildRequest() types.CephBootstrapRequest {
	return types.CephBootstrapRequest{
		Target:           c.flagTarget,
		MonIp:            c.flagMonIp,
		PublicNet:        c.flagPubNet,
		ClusterNet:       c.flagClusterNet,
		V2Only:           c.flagV2Only,
		AvailabilityZone: c.flagAvailabilityZone,
		Force:            c.flagForce,
	}
}
