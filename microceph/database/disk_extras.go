package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/state"
)

// MemberCounterInterface is for counting member nodes. Introduced for mocking.
//
//go:generate mockery --name MemberCounterInterface
type MemberCounterInterface interface {
	Count(s *state.State) (int, error)
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

// MembersDiskCnt returns the number of disks per member for all members that have at least one disk
func MembersDiskCnt(ctx context.Context, tx *sql.Tx) ([]MemberDisk, error) {
	var err error
	var sqlStmt *sql.Stmt

	objects := make([]MemberDisk, 0)

	sqlStmt, err = cluster.Stmt(tx, membersDiskCnt)
	if err != nil {
		return nil, fmt.Errorf("Failed to get \"membersDiskCnt\" prepared statement: %w", err)
	}

	dest := func(scan func(dest ...any) error) error {
		m := MemberDisk{}
		err := scan(&m.Member, &m.NumDisks)
		if err != nil {
			return err
		}
		objects = append(objects, m)
		return nil
	}

	err = query.SelectObjects(ctx, sqlStmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed to get \"membersDiskCnt\" objects: %w", err)
	}

	return objects, err
}

// Count returns the number of nodes in the cluster with at least one disk
func (m MemberCounterImpl) Count(s *state.State) (int, error) {
	var numNodes int

	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		records, err := MembersDiskCnt(ctx, tx)
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
