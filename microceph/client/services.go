// Package client provides a full Go API client.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/clilogger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
)

// GetServices returns the list of configured ceph services.
func GetServices(ctx context.Context, c mcTypes.Client) (types.Services, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	services := types.Services{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, &api.NewURL().Path("services").URL, nil, &services)
	if err != nil {
		return nil, fmt.Errorf("failed listing services: %w", err)
	}

	return services, nil
}

// DeleteService requests MicroCeph deconfigures a service on a given target node.
func DeleteService(ctx context.Context, c mcTypes.Client, target string, service string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Send this request to target.
	c = c.UseTarget(target)

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, &api.NewURL().Path("services", service).URL, nil, nil)
	if err != nil {
		return fmt.Errorf("failed disabling service %s: %w", service, err)
	}

	return nil
}

// DeleteNFSService requests MicroCeph to deconfigure the NFS service on a given target node.
func DeleteNFSService(ctx context.Context, c mcTypes.Client, target string, svc *types.NFSService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Send this request to target.
	c = c.UseTarget(target)

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, &api.NewURL().Path("services", "nfs").URL, svc, nil)
	if err != nil {
		return fmt.Errorf("failed deleting NFS service: %w", err)
	}

	return nil
}

// Send a request to start certain service at the target node (hostname for remote target).
func SendServicePlacementReq(ctx context.Context, c mcTypes.Client, data *types.EnableService, target string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Send this request to target.
	c = c.UseTarget(target)

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, &api.NewURL().Path("services", data.Name).URL, data, nil)
	if err != nil {
		return fmt.Errorf("failed placing service %s: %w", data.Name, err)
	}

	return nil
}

// ApplySMBSpec submits an SMBSpec JSON document for cluster-wide apply.
func ApplySMBSpec(ctx context.Context, c mcTypes.Client, spec []byte) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, &api.NewURL().Path("services", "smb").URL, json.RawMessage(spec), nil)
	if err != nil {
		return fmt.Errorf("failed applying smb spec: %w", err)
	}

	return nil
}

// RemoveSMBService removes an smb cluster from all its member nodes.
func RemoveSMBService(ctx context.Context, c mcTypes.Client, svc *types.SMBService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, &api.NewURL().Path("services", "smb").URL, svc, nil)
	if err != nil {
		return fmt.Errorf("failed removing smb cluster: %w", err)
	}

	return nil
}

// GetSMBServices lists every smb cluster with its spec and placement.
func GetSMBServices(ctx context.Context, c mcTypes.Client) ([]types.SMBServiceStatus, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	statuses := []types.SMBServiceStatus{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, &api.NewURL().Path("services", "smb").URL, nil, &statuses)
	if err != nil {
		return nil, fmt.Errorf("failed listing smb services: %w", err)
	}

	return statuses, nil
}

// EnableSMBNodeService requests the target node run the smb placement flow.
func EnableSMBNodeService(ctx context.Context, c mcTypes.Client, target string, data *types.EnableService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Send this request to target.
	c = c.UseTarget(target)

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, &api.NewURL().Path("services", "smb", "node").URL, data, nil)
	if err != nil {
		return fmt.Errorf("failed placing smb service on %s: %w", target, err)
	}

	return nil
}

// DeleteSMBNodeService requests the target node tear down its smb cluster
// membership.
func DeleteSMBNodeService(ctx context.Context, c mcTypes.Client, target string, svc *types.SMBService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Send this request to target.
	c = c.UseTarget(target)

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, &api.NewURL().Path("services", "smb", "node").URL, svc, nil)
	if err != nil {
		return fmt.Errorf("failed deleting smb service on %s: %w", target, err)
	}

	return nil
}

// Sends a request to the host to restart the provided service.
func RestartService(ctx context.Context, c mcTypes.Client, data *types.Services) error {
	// 120 second timeout for waiting.
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, &api.NewURL().Path("services", "restart").URL, data, nil)
	if err != nil {
		url := c.URL()
		return fmt.Errorf("failed Forwarding To: %s: %w", url.String(), err)
	}

	return nil
}

// Sends the desired list of services to be restarted on every other member of the cluster.
func SendRestartRequestToClusterMembers(ctx context.Context, s mcTypes.State, services []string) error {
	// Populate the restart request data.
	var data types.Services
	for _, service := range services {
		data = append(data, types.Service{Service: service})
	}

	// Get a collection of clients to every other cluster member, with the notification user-agent set.
	cluster, err := s.Connect().Cluster(false)
	if err != nil {
		clilogger.Errorf("failed to get a client for every cluster member: %v", err)
		return err
	}

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = RestartService(ctx, remoteClient, &data)
		if err != nil {
			clilogger.Errorf("restart error: %v", err)
			return err
		}
	}

	return nil
}
