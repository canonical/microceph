package api

import (
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
)

var Servers = map[string]mcTypes.Server{
	"microceph": {
		CoreAPI:   true,
		ServeUnix: true,
		Resources: []mcTypes.Resources{
			{
				PathPrefix: types.ExtendedPathPrefix,
				Endpoints: []mcTypes.Endpoint{
					disksCmd,
					disksDelCmd,
					resourcesCmd,
					servicesCmd,
					configsCmd,
					restartServiceCmd,
					mdsServiceCmd,
					mgrServiceCmd,
					monServiceCmd,
					nfsServiceCmd,
					poolsOpCmd,
					rgwServiceCmd,
					rbdMirroServiceCmd,
					fsMirroServiceCmd,
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
					// Maintenance APIs
					opsMaintenanceNodeCmd,
					// Certificate APIs
					certificatesRGWCmd,
				},
			},
		},
	},
}
