package client

import (
	"context"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
)

// ClientInterface wraps client functions
// This is useful for mocking in unit tests
type ClientInterface interface {
	GetClusterMembers(mcTypes.Client) ([]string, error)
	GetDisks(mcTypes.Client) (types.Disks, error)
	GetServices(mcTypes.Client) (types.Services, error)
	DeleteService(mcTypes.Client, string, string) error
	DeleteClusterMember(mcTypes.Client, string, bool) error
}

type ClientImpl struct{}

// GetClusterMembers gets the cluster member names
// We return names only here because the Member type is internal to microclient
func (c ClientImpl) GetClusterMembers(cli mcTypes.Client) ([]string, error) {
	var members []mcTypes.ClusterMember
	err := cli.Query(context.Background(), "GET", mcTypes.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, &members)
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
func (c ClientImpl) GetDisks(cli mcTypes.Client) (types.Disks, error) {
	return GetDisks(context.Background(), cli)
}

// GetServices wraps the GetServices function above
func (c ClientImpl) GetServices(cli mcTypes.Client) (types.Services, error) {
	return GetServices(context.Background(), cli)
}

// DeleteService wraps the DeleteService function
func (c ClientImpl) DeleteService(cli mcTypes.Client, target string, service string) error {
	return DeleteService(context.Background(), cli, target, service)
}

// DeleteClusterMember wraps the DeleteClusterMember function
func (c ClientImpl) DeleteClusterMember(cli mcTypes.Client, name string, force bool) error {
	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}
	return cli.Query(context.Background(), "DELETE", mcTypes.PublicEndpoint, &endpoint.URL, nil, nil)
}

// mocking point for unit tests
var MClient ClientInterface = ClientImpl{}
