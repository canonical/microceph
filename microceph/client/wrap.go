package client

import (
	"context"
	"time"

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

// clusterQueryTimeout is the deadline for the microcluster queries in this file.
//
// The queries must not run on a context without a deadline. If they do,
// microcluster's rawQuery adds a 30 second timeout of its own and cancels it as
// soon as the response headers are in, before Query has read the body, so the
// call fails with "context canceled" (or "use of closed network connection")
// whenever the body arrives a little later than the headers. With a deadline
// already set, rawQuery adds nothing, and the cancel deferred here runs only
// after Query has returned. 30 seconds is what rawQuery would have used.
const clusterQueryTimeout = 30 * time.Second

type ClientImpl struct{}

// GetClusterMembers gets the cluster member names
// We return names only here because the Member type is internal to microclient
func (c ClientImpl) GetClusterMembers(cli mcTypes.Client) ([]string, error) {
	// See clusterQueryTimeout: Query must not be given a context without a deadline.
	queryCtx, cancel := context.WithTimeout(context.Background(), clusterQueryTimeout)
	defer cancel()

	var members []mcTypes.ClusterMember
	err := cli.Query(queryCtx, "GET", mcTypes.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, &members)
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

	// See clusterQueryTimeout: Query must not be given a context without a deadline.
	queryCtx, cancel := context.WithTimeout(context.Background(), clusterQueryTimeout)
	defer cancel()

	return cli.Query(queryCtx, "DELETE", mcTypes.PublicEndpoint, &endpoint.URL, nil, nil)
}

// mocking point for unit tests
var MClient ClientInterface = ClientImpl{}
