package ceph

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

type smbRecoveryClient struct {
	Name    string
	Keyring string
}

// getSMBRecoveryClients projects only local clustered reservations into the
// canonical config. Key material remains in the private keyring; merely finding
// a retained keyring on disk never authorizes a client stanza.
func getSMBRecoveryClients(ctx context.Context, s interfaces.StateInterface) ([]smbRecoveryClient, error) {
	member := s.ClusterState().Name()
	if member == "" {
		return nil, nil // No local member exists before bootstrap.
	}
	var groups []string
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		groups, err = database.GetSMBRecoveryGroups(ctx, tx, member)
		return err
	})
	if err != nil {
		return nil, err
	}
	clients := make([]smbRecoveryClient, 0, len(groups))
	for _, group := range groups {
		err = validateSMBClusterID(group)
		if err != nil {
			return nil, err
		}
		clients = append(clients, smbRecoveryClient{
			Name:    "client.smb.cluster." + group,
			Keyring: filepath.Join(smbRoot(group), "identity/recovery.keyring"),
		})
	}
	return clients, nil
}
