package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var retireSMBCTDBMemberFunc = retireSMBCTDBMember
var getRecordedSMBModeFunc = getRecordedSMBMode

func getRecordedSMBMode(ctx context.Context, s interfaces.StateInterface, clusterID string) (*bool, error) {
	ctx, cancel := context.WithTimeout(ctx, smbCommandTimeout)
	defer cancel()
	var mode *bool
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		mode, err = database.GetSMBPlacementMode(ctx, tx, clusterID)
		return err
	})
	return mode, err
}

// retireSMBCTDBMember runs after local daemons have stopped, while the metadata
// and keyring still exist. The existing CTDB monitors reload the retired slot.
func retireSMBCTDBMember(ctx context.Context, clusterID string) error {
	dir := filepath.Join(filepath.Dir(constants.GetPathConst().ConfPath), "samba")
	rankData, err := os.ReadFile(filepath.Join(dir, "ctdb-rank"))
	if err != nil {
		return fmt.Errorf("failed reading CTDB rank for removal: %w", err)
	}
	rank, err := strconv.Atoi(strings.TrimSpace(string(rankData)))
	if err != nil || rank < 0 {
		return fmt.Errorf("invalid CTDB rank for removal")
	}
	identityData, err := os.ReadFile(filepath.Join(dir, "ctdb-identity"))
	if err != nil {
		return fmt.Errorf("failed reading CTDB identity for removal: %w", err)
	}
	identity := strings.TrimSpace(string(identityData))
	if identity == "" {
		return fmt.Errorf("missing CTDB identity for removal")
	}
	_, err = runSMBCommand(ctx, filepath.Join(os.Getenv("SNAP"), "bin", "python3"),
		filepath.Join(os.Getenv("SNAP"), "commands", "ctdb_ready.py"),
		fmt.Sprintf("rados://.smb/%s/cluster.meta.json", clusterID), clusterID, identity, strconv.Itoa(rank), "--state", "gone")
	if err != nil {
		return fmt.Errorf("failed retiring CTDB membership: %w", err)
	}
	return nil
}
