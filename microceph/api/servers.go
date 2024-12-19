package api

import (
	"github.com/canonical/microcluster/v2/rest"

	"github.com/canonical/microceph/microceph/api/types"
)

var Servers = map[string]rest.Server{
	"microceph": {
		CoreAPI:   true,
		ServeUnix: true,
		Resources: []rest.Resources{
			{
				PathPrefix: types.ExtendedPathPrefix,
				Endpoints: []rest.Endpoint{
					disksCmd,
					disksDelCmd,
					resourcesCmd,
					servicesCmd,
					configsCmd,
					restartServiceCmd,
					mdsServiceCmd,
					mgrServiceCmd,
					monServiceCmd,
					poolsOpCmd,
					rgwServiceCmd,
					rbdMirroServiceCmd,
					poolsCmd,
					clientCmd,
					clientConfigsCmd,
					clientConfigsKeyCmd,
					microcephCmd,
					microcephConfigsCmd,
					logLevelCmd,
					clusterCmd,
					remoteCmd,
					remoteNameCmd,
					opsCmd,
					// Remote Replication APIs
					opsReplicationCmd,
					opsReplicationWorkloadCmd,
					opsReplicationResourceCmd,
					// OSDs APIs
					osdCmd,
				},
			},
		},
	},
}
