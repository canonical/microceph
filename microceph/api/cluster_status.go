package api

import "github.com/canonical/microcluster/v2/rest"

var clusterStatusCmd = rest.Endpoint{
	Name: "Cluster Status Endpoint",
	Path: "cluster/status",
	Get:  rest.EndpointAction{},
}
