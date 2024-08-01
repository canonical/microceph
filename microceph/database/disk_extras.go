package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/v2/cluster"
	"github.com/canonical/microcluster/v2/state"
)

// MemberCounterInterface is for counting member nodes. Introduced for mocking.
//
//go:generate mockery --name MemberCounterInterface
type MemberCounterInterface interface {
	Count(s *state.State) (int, error)
	CountExclude(s *state.State, exclude int64) (int, error)
}

type MemberCounterImpl struct{}

type MemberDisk struct {
	Member   string `db:"member"`
	NumDisks int    `db:"num_disks"`
}

var _ = api.ServerEnvironment{}

var membersDiskCnt = cluster.RegisterStmt(`
SELECT internal_cluster_members.name AS member, count(disks.id) AS num_disks 
  FROM disks
  JOIN internal_cluster_members ON disks.member_id = internal_cluster_members.id 
  GROUP BY internal_cluster_members.id 
`)

var membersDiskCntExclude = cluster.RegisterStmt(`
SELECT internal_cluster_members.name AS member, count(disks.id) AS num_disks
FROM disks
JOIN internal_cluster_members ON disks.member_id = internal_cluster_members.id
WHERE disks.id != ?
GROUP BY internal_cluster_members.id
`)

// MembersDiskCnt returns the number of disks per member for all members that have at least one disk excluding the given OSD
func MembersDiskCnt(ctx context.Context, tx *sql.Tx, exclude int64) ([]MemberDisk, error) {
	var err error
	var sqlStmt *sql.Stmt

	objects := make([]MemberDisk, 0)

	dest := func(scan func(dest ...any) error) error {
		m := MemberDisk{}
		err := scan(&m.Member, &m.NumDisks)
		if err != nil {
			return err
		}
		objects = append(objects, m)
		return nil
	}

	if exclude == -1 {
		sqlStmt, err = cluster.Stmt(tx, membersDiskCnt)
		if err != nil {
			return nil, fmt.Errorf("Failed to get \"membersDiskCnt\" prepared statement: %w", err)
		}

		err = query.SelectObjects(ctx, sqlStmt, dest)
		if err != nil {
			return nil, fmt.Errorf("Failed to get \"membersDiskCnt\" objects: %w", err)
		}
	} else {
		sqlStmt, err = cluster.Stmt(tx, membersDiskCntExclude)
		if err != nil {
			return nil, fmt.Errorf("Failed to get \"membersDiskCntExclude\" prepared statement: %w", err)
		}

		err = query.SelectObjects(ctx, sqlStmt, dest, exclude)
		if err != nil {
			return nil, fmt.Errorf("Failed to get \"membersDiskCntExclude\" objects: %w", err)
		}
	}
	return objects, err
}

// Count returns the number of nodes in the cluster with at least one disk
func (m MemberCounterImpl) Count(s *state.State) (int, error) {
	var numNodes int

	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		records, err := MembersDiskCnt(ctx, tx, -1)
		if err != nil {
			return fmt.Errorf("Failed to fetch disks: %w", err)
		}
		numNodes = len(records)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return numNodes, nil
}

// CountExclude returns the number of nodes in the cluster with at least one disk, excluding the given OSD
func (m MemberCounterImpl) CountExclude(s *state.State, exclude int64) (int, error) {
	var numNodes int

	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		records, err := MembersDiskCnt(ctx, tx, exclude)
		if err != nil {
			return fmt.Errorf("Failed to fetch disks: %w", err)
		}
		numNodes = len(records)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return numNodes, nil
}

// Singleton for the MemberCounterImpl, to be mocked in unit testing
var MemberCounter MemberCounterInterface = MemberCounterImpl{}

// OSDQueryInterface is for querying OSDs. Introduced for mocking.
type OSDQueryInterface interface {
	HaveOSD(s *state.State, osd int64) (bool, error)
	Path(s *state.State, osd int64) (string, error)
	Delete(s *state.State, osd int64) error
	List(s *state.State) (types.Disks, error)
	UpdatePath(s *state.State, osd int64, path string) error
}

type OSDQueryImpl struct{}

var haveOsd = cluster.RegisterStmt(`
SELECT count(*)
FROM disks
WHERE disks.id = ?
`)

var osdPath = cluster.RegisterStmt(`
SELECT disks.path
FROM disks
WHERE disks.id = ?
`)

var updatePath = cluster.RegisterStmt(`
UPDATE disks
SET path = ?
WHERE disks.id = ?
`)

// HaveOSD returns either false or true depending on whether the given OSD is present in the cluster
func (o OSDQueryImpl) HaveOSD(s *state.State, osd int64) (bool, error) {
	var present int

	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		sqlStmt, err := cluster.Stmt(tx, haveOsd)
		if err != nil {
			return fmt.Errorf("Failed to get \"haveOsd\" prepared statement: %w", err)
		}

		err = sqlStmt.QueryRow(osd).Scan(&present)
		if err != nil {
			return fmt.Errorf("Failed to get \"haveOsd\" objects: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return present > 0, nil
}

// Path returns the path of the given OSD
func (o OSDQueryImpl) Path(s *state.State, osd int64) (string, error) {
	var path string

	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		sqlStmt, err := cluster.Stmt(tx, osdPath)
		if err != nil {
			return fmt.Errorf("Failed to get \"osdPath\" prepared statement: %w", err)
		}

		err = sqlStmt.QueryRow(osd).Scan(&path)
		if err != nil {
			return fmt.Errorf("Failed to get \"osdPath\" objects: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// Delete OSD records for the given OSD
func (o OSDQueryImpl) Delete(s *state.State, osd int64) error {
	path, err := o.Path(s, osd)
	if err != nil {
		return err
	}
	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		return DeleteDisk(ctx, tx, s.Name(), path)
	})
	return err
}

// List OSD records
func (o OSDQueryImpl) List(s *state.State) (types.Disks, error) {
	disks := types.Disks{}
	// Get the OSDs from the database.
	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		records, err := GetDisks(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch disks: %w", err)
		}

		for _, disk := range records {
			disks = append(disks, types.Disk{
				OSD:      int64(disk.ID),
				Location: disk.Member,
				Path:     disk.Path,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	return disks, nil
}

// UpdatePath updates the path of the given OSD
func (o OSDQueryImpl) UpdatePath(s *state.State, osd int64, path string) error {
	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		sqlStmt, err := cluster.Stmt(tx, updatePath)
		if err != nil {
			return fmt.Errorf("failed to get \"updatePath\" prepared statement: %w", err)
		}

		_, err = sqlStmt.Exec(path, osd)
		if err != nil {
			return fmt.Errorf("failed to get \"updatePath\" objects: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Singleton for the OSDQueryImpl, to be mocked in unit testing
var OSDQuery OSDQueryInterface = OSDQueryImpl{}
