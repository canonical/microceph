package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/v3/microcluster/types"
)

// ClientInterface wraps client functions
// This is useful for mocking in unit tests
type ClientInterface interface {
	GetClusterMembers(microCli.Client) ([]string, error)
	GetDisks(microCli.Client) (types.Disks, error)
	GetServices(microCli.Client) (types.Services, error)
	DeleteService(microCli.Client, string, string) error
	DeleteClusterMember(microCli.Client, string, bool) error
}

type ClientImpl struct{}

// GetClusterMembers gets the cluster member names
// We return names only here because the Member type is internal to microclient
func (c ClientImpl) GetClusterMembers(cli microCli.Client) ([]string, error) {
	queryCtx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	var members []microCli.ClusterMember
	err := cli.Query(queryCtx, "GET", microCli.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, &members)
	if err != nil {
		return nil, err
	}

	memberNames := make([]string, 0, len(members))
	for _, member := range members {
		memberNames = append(memberNames, member.Name)
	}

	return memberNames, nil
}

// GetDisks wraps the GetDisks function above
func (c ClientImpl) GetDisks(cli microCli.Client) (types.Disks, error) {
	return GetDisks(context.Background(), cli)
}

// GetServices wraps the GetServices function above
func (c ClientImpl) GetServices(cli microCli.Client) (types.Services, error) {
	return GetServices(context.Background(), cli)
}

// DeleteService wraps the DeleteService function
func (c ClientImpl) DeleteService(cli microCli.Client, target string, service string) error {
	return DeleteService(context.Background(), cli, target, service)
}

// DeleteClusterMember wraps the DeleteClusterMember function
func (c ClientImpl) DeleteClusterMember(cli microCli.Client, name string, force bool) error {
	queryCtx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return cli.Query(queryCtx, "DELETE", microCli.PublicEndpoint, &endpoint.URL, nil, nil)
}

// mocking point for unit tests
var MClient ClientInterface = ClientImpl{}
