package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
)

// /1.0/resources endpoint.
var clientS3Cmd = rest.Endpoint{
	Path:   "client/s3",
	Get:    rest.EndpointAction{Handler: cmdClientS3Get, ProxyTarget: true},
	Put:    rest.EndpointAction{Handler: cmdClientS3Put, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdClientS3Delete, ProxyTarget: true},
}

func cmdClientS3Get(s *state.State, r *http.Request) response.Response {
	var err error
	var req types.S3User

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// If a user name is passed.
	if len(req.Name) > 0 {
		getOutput, err := ceph.GetS3User(req)
		if err != nil {
			return response.SmartError(err)
		}
		return response.SyncResponse(true, getOutput)
	} else {
		listOutput, err := ceph.ListS3Users()
		if err != nil {
			return response.SmartError(err)
		}
		return response.SyncResponse(true, listOutput)
	}
}

func cmdClientS3Put(s *state.State, r *http.Request) response.Response {
	var err error
	var req types.S3User

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	output, err := ceph.CreateS3User(req)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, output)
}

func cmdClientS3Delete(s *state.State, r *http.Request) response.Response {
	var err error
	var req types.S3User

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	err = ceph.DeleteS3User(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
