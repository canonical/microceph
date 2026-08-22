package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The API object id is not cosmetic: an empty one routes a request to the
// workload root endpoint, which can only ever fire the cluster wide events.
// A site scoped verb that returns an empty id would silently be answered as a
// list, so each verb's id is pinned here.

func TestRgwGetAPIObjectIDSiteScopedVerbs(t *testing.T) {
	for _, requestType := range []ReplicationRequestType{
		StatusReplicationRequest,
		EnableReplicationRequest,
		DisableReplicationRequest,
		ConfigureReplicationRequest,
	} {
		req := RgwReplicationRequest{RequestType: requestType, ResourceType: RgwResourceSite}
		assert.Equal(t, "site", req.GetAPIObjectID(), "request type %s", requestType)
	}
}

func TestRgwGetAPIObjectIDClusterWideVerbs(t *testing.T) {
	for _, requestType := range []ReplicationRequestType{
		ListReplicationRequest,
		PromoteReplicationRequest,
		DemoteReplicationRequest,
		WorkloadReplicationRequest,
	} {
		req := RgwReplicationRequest{RequestType: requestType}
		assert.Empty(t, req.GetAPIObjectID(), "request type %s", requestType)
	}
}

func TestRgwGetAPIObjectIDBucketScoped(t *testing.T) {
	req := RgwReplicationRequest{
		RequestType:  StatusReplicationRequest,
		ResourceType: RgwResourceBucket,
		Bucket:       "my-bucket.photos",
	}

	assert.Equal(t, "my-bucket.photos", req.GetAPIObjectID())
}

func TestRgwSetAPIObjectIDSiteSentinelCarriesNoData(t *testing.T) {
	req := RgwReplicationRequest{ResourceType: RgwResourceSite}

	err := req.SetAPIObjectID("site")
	assert.NoError(t, err)
	assert.Empty(t, req.Bucket)
	assert.Equal(t, RgwResourceSite, req.ResourceType)
}

func TestRgwSetAPIObjectIDBucket(t *testing.T) {
	req := RgwReplicationRequest{ResourceType: RgwResourceBucket}

	err := req.SetAPIObjectID("my-bucket.photos")
	assert.NoError(t, err)
	assert.Equal(t, "my-bucket.photos", req.Bucket)
}

// A bucket that happens to be named "site" is scoped by the request body, not
// by the path segment it shares with the sentinel.
func TestRgwSetAPIObjectIDBucketNamedSite(t *testing.T) {
	req := RgwReplicationRequest{ResourceType: RgwResourceBucket}

	err := req.SetAPIObjectID("site")
	assert.NoError(t, err)
	assert.Equal(t, "site", req.Bucket)
	assert.Equal(t, RgwResourceBucket, req.ResourceType)
}

func TestRgwRequestTypeAccessors(t *testing.T) {
	req := RgwReplicationRequest{RequestType: StatusReplicationRequest}

	assert.Equal(t, RgwWorkload, req.GetWorkloadType())
	assert.Equal(t, "GET", req.GetAPIRequestType())
	assert.Equal(t, "status_replication", req.GetWorkloadRequestType())
}

func TestRgwOverwriteRequestType(t *testing.T) {
	req := RgwReplicationRequest{RequestType: StatusReplicationRequest}

	// An empty overwrite is the cluster wide case and must not clobber the
	// request type the client already encoded.
	req.OverwriteRequestType(WorkloadReplicationRequest)
	assert.Equal(t, StatusReplicationRequest, req.RequestType)

	req.OverwriteRequestType(EnableReplicationRequest)
	assert.Equal(t, EnableReplicationRequest, req.RequestType)
}
