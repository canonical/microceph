package database

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/canonical/microcluster/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
)

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
