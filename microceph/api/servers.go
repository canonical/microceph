package api

import (
	"github.com/canonical/microcluster/rest"

	"github.com/canonical/microceph/microceph/api/types"
)

var Servers = []rest.Server{
	{
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
					opsReplicationCmd,
					opsReplicationRbdCmd,
				},
			},
		},
	},
}
