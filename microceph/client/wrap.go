package client

import (
	"context"
	"github.com/canonical/microceph/microceph/api/types"

	microCli "github.com/canonical/microcluster/client"
)

// ClientInterface wraps client functions
// This is useful for mocking in unit tests
type ClientInterface interface {
	GetClusterMembers(*microCli.Client) ([]string, error)
	GetDisks(*microCli.Client) (types.Disks, error)
	GetServices(*microCli.Client) (types.Services, error)
	DeleteService(*microCli.Client, string, string) error
}

type ClientImpl struct{}

// GetClusterMembers gets the cluster member names
// We return names only here because the Member type is internal to microclient
func (c ClientImpl) GetClusterMembers(cli *microCli.Client) ([]string, error) {
	memberNames := make([]string, 3)
	members, err := cli.GetClusterMembers(context.Background())
	if err != nil {
		return nil, err
	}

	for _, member := range members {
		memberNames = append(memberNames, member.Name)
	}

	return memberNames, nil
}

// GetDisks wraps the GetDisks function above
func (c ClientImpl) GetDisks(cli *microCli.Client) (types.Disks, error) {
	return GetDisks(context.Background(), cli)
}

// GetServices wraps the GetServices function above
func (c ClientImpl) GetServices(cli *microCli.Client) (types.Services, error) {
	return GetServices(context.Background(), cli)
}

// DeleteService wraps the DeleteService function
func (c ClientImpl) DeleteService(cli *microCli.Client, target string, service string) error {
	return DeleteService(context.Background(), cli, target, service)
}

// mocking point for unit tests
var MClient ClientInterface = ClientImpl{}
