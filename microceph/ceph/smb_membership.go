package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/canonical/microceph/microceph/logger"
)

var prepareSMBCTDBFilesFunc = prepareSMBCTDBFiles

func startSMBMembershipUpdater(ctx context.Context) {
	base := smbBase()
	changes := smbMembershipChanges
	go func() {
		ticker := time.NewTicker(smbUpdateInterval)
		defer ticker.Stop()
		runSMBMembershipUpdater(ctx, base, changes, ticker.C)
	}()
}

func runSMBMembershipUpdater(ctx context.Context, base string, changes <-chan struct{}, fallback <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
		case <-fallback:
		}
		err := reconcileSMBCTDB(ctx, base)
		if err != nil && ctx.Err() == nil {
			logger.Warn("SMB membership is not confirmed; inspect private diagnostics")
		}
	}
}

// Membership-only publication cannot grant account preparation or change config.
func publishSMBMembership(ctx context.Context, root string, candidate smbCandidate) error {
	smbMembershipMu.Lock()
	defer smbMembershipMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	previous, err := readSMBPublishedCandidate(root, candidate.Spec.ClusterID)
	if err != nil {
		return err
	}
	if previous.CTDB == nil || candidate.CTDB == nil || !reflect.DeepEqual(previous.CTDB.Member, candidate.CTDB.Member) || previous.configurationFingerprint() != candidate.configurationFingerprint() {
		return fmt.Errorf("SMB membership update conflicts with local identity or configuration")
	}
	if reflect.DeepEqual(previous.CTDB, candidate.CTDB) {
		return nil
	}
	err = publishSMBCTDBLayout(root, true)
	if err != nil {
		return err
	}
	_, err = smbCTDBNodes(candidate)
	if err != nil {
		return err
	}
	err = smbAtomicWrite(filepath.Join(root, "membership-update-pending"), []byte("membership publication is not confirmed applied\n"))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	err = smbAtomicWrite(filepath.Join(root, "config/candidate.json"), raw)
	if err != nil {
		return err
	}
	notifySMBMembershipChangeFunc()
	return nil
}

func completeSMBMembership(root string) {
	_ = os.Remove(filepath.Join(root, "membership-update-pending"))
}
