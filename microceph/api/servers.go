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
					rgwServiceCmd,
					configsCmd,
					restartServiceCmd,
					mdsServiceCmd,
					mgrServiceCmd,
					monServiceCmd,
					poolsOpCmd,
					poolsCmd,
					clientCmd,
					clientConfigsCmd,
					clientConfigsKeyCmd,
					microcephCmd,
					microcephConfigsCmd,
					logLevelCmd,
				},
			},
		},
	},
}
