package ceph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	lxdApi "github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	microCluster "github.com/canonical/microcluster/v2/cluster"
	microTypes "github.com/canonical/microcluster/v2/rest/types"
)

var (
	syncTrustStoreFromDatabaseOp = SyncTrustStoreFromDatabase
	reconcileMonHostEntriesOp    = reconcileMonHostEntries
	updateConfigOp               = UpdateConfig
	syncClusterRemotesOnPeersOp  = syncClusterRemotesOnPeers
)

// ForceRemoveClusterMember is the recovery-path member removal used when the normal
// cluster delete API is blocked by microcluster's upgrade-waiting database state.
//
// Key invariants for safety:
//   - membership source-of-truth is core_cluster_members (database), not trust-store,
//   - we never remove the local member through this API,
//   - if no actual cleanup happened, we report NotFound instead of silently succeeding.
//
// After membership cleanup, the function reconciles trust-store and monitor-host config
// so local/peer config generation converges to current DB membership.
func ForceRemoveClusterMember(ctx context.Context, s interfaces.StateInterface, memberName string) error {
	state := s.ClusterState()

	if memberName == state.Name() {
		return fmt.Errorf("refusing to force remove local member %q", memberName)
	}

	// Trust-store data can be stale during recovery scenarios. We capture it only as
	// a fallback signal for dqlite cleanup when the DB member row no longer exists.
	trustStoreAddress := ""
	if remote, ok := state.Remotes().RemotesByName()[memberName]; ok {
		trustStoreAddress = remote.Address.String()
	}

	leader, err := state.Database().Leader(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to dqlite leader: %w", err)
	}

	nodes, err := state.Database().Cluster(ctx, leader)
	if err != nil {
		return fmt.Errorf("failed to list dqlite cluster members: %w", err)
	}

	var (
		// Address to use for dqlite member removal after SQL transaction commits.
		dqliteTargetAddress string
		// Whether the member existed in core_cluster_members.
		dbMemberFound bool
		// Whether explicit mon.host.<member> cleanup removed an entry.
		removedNamedMonHost bool
		// Whether dqlite member removal succeeded.
		removedDqliteNode bool
	)

	// Perform database-side cleanup atomically: remove DB member row (when present)
	// and prune direct mon.host.<member> entry.
	err = state.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		recoveryMembers, err := getClusterMembersForRecovery(ctx, tx)
		if err != nil {
			return err
		}

		members := make([]microCluster.CoreClusterMember, 0, len(recoveryMembers))
		for _, member := range recoveryMembers {
			members = append(members, microCluster.CoreClusterMember{Name: member.Name, Address: member.Address, Role: member.Role})
		}

		// Prefer database identity. Only use trust-store fallback for dqlite cleanup
		// when the member is missing from DB and the address is not reused by another member.
		dbAddress, dqliteAddress, found := resolveRemovalAddresses(memberName, members, trustStoreAddress)
		dqliteTargetAddress = dqliteAddress
		dbMemberFound = found

		if !dbMemberFound && trustStoreAddress != "" && dqliteTargetAddress == "" {
			logger.Warnf("force remove: trust-store address %q for %q belongs to another database member; skipping dqlite fallback", trustStoreAddress, memberName)
		}

		if dbMemberFound {
			err = ensureRemovalLeavesCluster(memberName, members)
			if err != nil {
				return err
			}

			if trustStoreAddress != "" && trustStoreAddress != dbAddress {
				logger.Warnf("force remove: trust-store address %q for %q differs from database address %q; using database address", trustStoreAddress, memberName, dbAddress)
			}

			_, err = deleteCoreClusterMemberByAddressRaw(ctx, tx, dbAddress)
			if err != nil {
				return err
			}
		}

		monHostKey := fmt.Sprintf("mon.host.%s", memberName)
		removedNamedMonHost, err = deleteConfigItemByKeyRaw(ctx, tx, monHostKey)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to remove cluster member %q from database: %w", memberName, err)
	}

	var dqliteErr error
	if dqliteTargetAddress != "" {
		dqliteIndex := -1
		for i, node := range nodes {
			if node.Address == dqliteTargetAddress {
				dqliteIndex = i
				break
			}
		}

		if dqliteIndex >= 0 {
			err = leader.Remove(ctx, nodes[dqliteIndex].ID)
			if err != nil {
				dqliteErr = fmt.Errorf("failed to remove member %q from dqlite: %w", memberName, err)
			} else {
				removedDqliteNode = true
			}
		} else {
			logger.Warnf("No dqlite record exists for %q at address %q, continuing with internal cleanup only", memberName, dqliteTargetAddress)
		}
	}

	// Avoid false-positive success on typos or fully missing members.
	if shouldReportForceRemoveNotFound(dbMemberFound, removedDqliteNode, removedNamedMonHost) {
		return lxdApi.StatusErrorf(http.StatusNotFound, "cluster member %q not found", memberName)
	}

	reconcileErr := reconcileAfterForceRemove(ctx, s)
	return wrapForceRemoveOutcomeError(memberName, dqliteErr, reconcileErr)
}

func wrapForceRemoveOutcomeError(memberName string, dqliteErr error, reconcileErr error) error {
	if dqliteErr == nil && reconcileErr == nil {
		return nil
	}

	return fmt.Errorf("force remove %q completed with partial failures: %w", memberName, errors.Join(dqliteErr, reconcileErr))
}

func reconcileAfterForceRemove(ctx context.Context, s interfaces.StateInterface) error {
	var reconcileErrors []error

	// Reconcile local files/config from the now-authoritative DB state.
	err := syncTrustStoreFromDatabaseOp(ctx, s)
	if err != nil {
		reconcileErrors = append(reconcileErrors, fmt.Errorf("failed to refresh trust-store from database: %w", err))
	}

	_, err = reconcileMonHostEntriesOp(ctx, s)
	if err != nil {
		reconcileErrors = append(reconcileErrors, fmt.Errorf("failed to reconcile monitor host entries: %w", err))
	}

	err = updateConfigOp(ctx, s)
	if err != nil {
		logger.Warnf("force remove: failed to regenerate ceph.conf locally: %v", err)
	}

	syncClusterRemotesOnPeersOp(ctx, s)

	return errors.Join(reconcileErrors...)
}

type recoveryClusterMember struct {
	Name        string
	Address     string
	Certificate string
	Role        microCluster.Role
}

func getClusterMembersTableName(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE name IN ('internal_cluster_members_new', 'core_cluster_members', 'internal_cluster_members')")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			return "", err
		}

		seen[name] = true
	}

	err = rows.Err()
	if err != nil {
		return "", err
	}

	if seen["internal_cluster_members_new"] {
		return "internal_cluster_members_new", nil
	}

	if seen["core_cluster_members"] {
		return "core_cluster_members", nil
	}

	if seen["internal_cluster_members"] {
		return "internal_cluster_members", nil
	}

	return "", fmt.Errorf("no cluster members table found")
}

func getClusterMembersForRecovery(ctx context.Context, tx *sql.Tx) ([]recoveryClusterMember, error) {
	tableName, err := getClusterMembersTableName(ctx, tx)
	if err != nil {
		return nil, err
	}

	stmt := fmt.Sprintf("SELECT name,address,certificate,role FROM %s ORDER BY name", tableName)
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []recoveryClusterMember{}
	for rows.Next() {
		member := recoveryClusterMember{}
		var role string

		err = rows.Scan(&member.Name, &member.Address, &member.Certificate, &role)
		if err != nil {
			return nil, err
		}

		member.Role = microCluster.Role(role)
		members = append(members, member)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return members, nil
}

func deleteCoreClusterMemberByAddressRaw(ctx context.Context, tx *sql.Tx, address string) (bool, error) {
	tableName, err := getClusterMembersTableName(ctx, tx)
	if err != nil {
		return false, err
	}

	stmt := fmt.Sprintf("DELETE FROM %s WHERE address = ?", tableName)
	result, err := tx.ExecContext(ctx, stmt, address)
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	if affected > 1 {
		return false, fmt.Errorf("query deleted %d cluster member rows instead of at most 1", affected)
	}

	return affected == 1, nil
}

func deleteConfigItemByKeyRaw(ctx context.Context, tx *sql.Tx, key string) (bool, error) {
	result, err := tx.ExecContext(ctx, "DELETE FROM config WHERE key = ?", key)
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	if affected > 1 {
		return false, fmt.Errorf("query deleted %d config rows instead of at most 1", affected)
	}

	return affected == 1, nil
}

func getConfigDbRawTx(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT key,value FROM config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	config := map[string]string{}
	for rows.Next() {
		var key string
		var value string

		err = rows.Scan(&key, &value)
		if err != nil {
			return nil, err
		}

		config[key] = value
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return config, nil
}

// resolveRemovalAddresses returns addresses used for removal operations.
//
// Behavior:
//   - If member exists in DB, use DB address for both SQL and dqlite cleanup.
//   - If member is absent in DB, optionally use trust-store address only for dqlite cleanup.
//   - Never use trust-store fallback if that address is currently owned by another DB member.
func resolveRemovalAddresses(memberName string, members []microCluster.CoreClusterMember, trustStoreAddress string) (dbAddress string, dqliteAddress string, dbMemberFound bool) {
	trustAddressInUseByAnotherMember := false

	for _, member := range members {
		if member.Name == memberName {
			dbAddress = member.Address
			dqliteAddress = member.Address
			dbMemberFound = true
			return dbAddress, dqliteAddress, dbMemberFound
		}

		if trustStoreAddress != "" && member.Address == trustStoreAddress {
			trustAddressInUseByAnotherMember = true
		}
	}

	if trustAddressInUseByAnotherMember {
		return "", "", false
	}

	return "", trustStoreAddress, false
}

// shouldReportForceRemoveNotFound determines whether recovery remove did nothing
// meaningful and should report NotFound instead of success.
func shouldReportForceRemoveNotFound(dbMemberFound bool, removedDqliteNode bool, removedNamedMonHost bool) bool {
	return !dbMemberFound && !removedDqliteNode && !removedNamedMonHost
}

func ensureRemovalLeavesCluster(memberName string, members []microCluster.CoreClusterMember) error {
	remainingNonPending := 0
	targetFound := false

	for _, member := range members {
		if member.Name == memberName {
			targetFound = true
			continue
		}

		if member.Role != microCluster.Pending {
			remainingNonPending++
		}
	}

	if !targetFound {
		// The caller may still perform other cleanup paths (trust-store/dqlite/config).
		logger.Warnf("No internal database record exists for %q, continuing with cleanup only", memberName)
		return nil
	}

	if remainingNonPending < 1 {
		return fmt.Errorf("cannot remove cluster member %q: no remaining non-pending members", memberName)
	}

	return nil
}

// SyncTrustStoreFromDatabase refreshes local trust-store YAMLs from current core_cluster_members.
func SyncTrustStoreFromDatabase(ctx context.Context, s interfaces.StateInterface) error {
	var dbMembers []recoveryClusterMember
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		dbMembers, err = getClusterMembersForRecovery(ctx, tx)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to fetch cluster members from database: %w", err)
	}

	apiMembers := make([]microTypes.ClusterMember, 0, len(dbMembers))
	for _, member := range dbMembers {
		address, err := microTypes.ParseAddrPort(member.Address)
		if err != nil {
			return fmt.Errorf("failed to parse address of cluster member %q for trust-store sync: %w", member.Name, err)
		}

		certificate, err := microTypes.ParseX509Certificate(member.Certificate)
		if err != nil {
			return fmt.Errorf("failed to parse certificate of cluster member %q for trust-store sync: %w", member.Name, err)
		}

		apiMembers = append(apiMembers, microTypes.ClusterMember{
			ClusterMemberLocal: microTypes.ClusterMemberLocal{
				Name:        member.Name,
				Address:     address,
				Certificate: *certificate,
			},
			Role: string(member.Role),
		})
	}

	if len(apiMembers) == 0 {
		return fmt.Errorf("refusing to sync trust-store with an empty member list")
	}

	err = s.ClusterState().Remotes().Replace(s.ClusterState().FileSystem().TrustDir, apiMembers...)
	if err != nil {
		return fmt.Errorf("failed to refresh trust-store entries: %w", err)
	}

	return nil
}

// ReconcileMonHostEntries removes stale mon.host.<member> keys for members no longer in the cluster.
// Indexed mon.host.<n> keys are preserved for adopt flows.
func ReconcileMonHostEntries(ctx context.Context, s interfaces.StateInterface) error {
	_, err := reconcileMonHostEntries(ctx, s)
	return err
}

// reconcileMonHostEntries performs stale mon.host.* cleanup and returns the number
// of removed keys for callers that need outcome-aware behavior.
func reconcileMonHostEntries(ctx context.Context, s interfaces.StateInterface) (int, error) {
	removed := 0

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		members, err := getClusterMembersForRecovery(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to fetch cluster members while reconciling monitor config: %w", err)
		}

		memberNames := make([]string, 0, len(members))
		for _, member := range members {
			memberNames = append(memberNames, member.Name)
		}

		config, err := getConfigDbRawTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to read config db while reconciling monitor config: %w", err)
		}

		staleKeys := staleMonHostKeys(config, memberNames)
		if len(staleKeys) == 0 {
			return nil
		}

		for _, key := range staleKeys {
			deleted, err := deleteConfigItemByKeyRaw(ctx, tx, key)
			if err != nil {
				return err
			}

			if deleted {
				removed++
			}
		}

		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to remove stale monitor config keys: %w", err)
	}

	if removed > 0 {
		logger.Infof("Removed %d stale monitor host entries", removed)
	}

	return removed, nil
}

// syncClusterRemotesOnPeers fans out a best-effort reconciliation request to peers.
// Failures are logged but never returned, because local recovery has already succeeded.
func syncClusterRemotesOnPeers(ctx context.Context, s interfaces.StateInterface) {
	const (
		// Keep bounded concurrency to avoid slow serial fan-out while also preventing
		// unbounded goroutine bursts on large clusters.
		maxConcurrentPeerSyncs = 4
		// Shorter per-peer timeout keeps the force-remove API latency bounded in
		// degraded networks.
		peerSyncTimeout = 30 * time.Second
	)

	clusterClients, err := s.ClusterState().Cluster(false)
	if err != nil {
		logger.Warnf("force remove: failed to get peer clients for trust-store sync: %v", err)
		return
	}

	semaphore := make(chan struct{}, maxConcurrentPeerSyncs)
	var waitGroup sync.WaitGroup

	for _, remoteClient := range clusterClients {
		remoteClient := remoteClient

		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			// Acquire bounded worker slot.
			semaphore <- struct{}{}
			defer func() {
				<-semaphore
			}()

			peerCtx, cancel := context.WithTimeout(ctx, peerSyncTimeout)
			defer cancel()

			err := client.SyncClusterRemotes(peerCtx, &remoteClient)
			if err != nil {
				peerURL := remoteClient.URL()
				logger.Warnf("force remove: failed to sync trust-store on peer %q: %v", peerURL.String(), err)
			}
		}()
	}

	waitGroup.Wait()
}
