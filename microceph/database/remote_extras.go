package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microcluster/state"
)

// GetRemoteDb fetches a single or all remotes (when name == "") from DB.
var GetRemoteDb = func(s state.State, name string) ([]Remote, error) {
	var remotes []Remote
	var err error

	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
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

var DeleteRemoteDb = func(s state.State, remoteName string) error {
	pathConst := constants.GetPathConst()

	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
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
