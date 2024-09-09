package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/state"
)

// PersistRemoteDb adds the remote record to dqlite.
var PersistRemoteDb = func(ctx context.Context, s interfaces.StateInterface, remote types.RemoteImportRequest) error {
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Record the remote.
		_, err := CreateRemote(ctx, tx, Remote{LocalName: remote.LocalName, Name: remote.Name})
		if err != nil {
			return fmt.Errorf("failed to record remote %s: %w", remote.Name, err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// GetRemoteDb fetches a single or all remotes (when name == "") from DB.
var GetRemoteDb = func(ctx context.Context, s state.State, name string) ([]Remote, error) {
	var remotes []Remote
	var err error

	err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Remove record to database.
		if len(name) == 0 {
			remotes, err = GetRemotes(ctx, tx)
			if err != nil {
				return fmt.Errorf("failed to add client config: %v", err)
			}
		} else {
			remote, err := GetRemote(ctx, tx, name)
			if err != nil {
				return fmt.Errorf("failed to add client config: %v", err)
			}
			// prepare a slice with single element.
			remotes = append(remotes, *remote)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return remotes, nil
}

var DeleteRemoteDb = func(ctx context.Context, s state.State, remoteName string) error {
	pathConst := constants.GetPathConst()

	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Remove record to database.
		err := DeleteRemote(ctx, tx, remoteName)
		if err != nil {
			return fmt.Errorf("failed to add client config: %v", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Remove remote conf and keyring files.
	err = os.Remove(filepath.Join(pathConst.ConfPath, fmt.Sprintf("%s.conf", remoteName)))
	if err != nil {
		return err
	}

	err = os.Remove(filepath.Join(pathConst.ConfPath, fmt.Sprintf("%s.keyring", remoteName)))
	if err != nil {
		return err
	}

	return nil
}
