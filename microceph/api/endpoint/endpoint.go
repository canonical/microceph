// Package endpoint defines the public MicroCeph API prefix shared by servers and clients.
package endpoint

import "github.com/canonical/microcluster/v2/rest/types"

// PublicEndpoint preserves the /1.0 application API path across Microcluster versions.
const PublicEndpoint types.EndpointPrefix = "1.0"
