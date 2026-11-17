package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// fakeCluster is an in-memory stand-in for the CRUSH rule state of a real
// cluster plus the cluster-wide failure domain transition lock. It makes the
// host-transition/rack-promotion interleaving of the serialization regression
// test deterministic: node A's transition suspends right after it captured
// the pool list (the exact point where the reported race window opens), and
// node B's promotion attempt runs while A is suspended.
type fakeCluster struct {
	mu         sync.Mutex
	ruleIDs    map[string]int
	nextRuleID int
	defaultRl  string
	pools      map[string]string
	lockHolder int64
	denied     int

	pauseArmed bool
	pauseHit   chan struct{}
	resume     chan struct{}
}

func newFakeCluster(pools ...string) *fakeCluster {
	fc := &fakeCluster{
		ruleIDs: map[string]int{
			"microceph_auto_osd":  1,
			"microceph_auto_host": 2,
			"microceph_auto_rack": 3,
		},
		nextRuleID: 4,
		defaultRl:  "microceph_auto_osd",
		pools:      map[string]string{},
		pauseHit:   make(chan struct{}, 1),
		resume:     make(chan struct{}),
	}
	for _, p := range pools {
		fc.pools[p] = "microceph_auto_osd"
	}
	return fc
}

// tryAcquire mirrors database.TryAcquireFailureDomainLock: a single atomic
// decision, busy when a live holder exists.
func (fc *fakeCluster) tryAcquire() (int64, bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lockHolder != 0 {
		fc.denied++
		return 0, false
	}
	token := time.Now().UnixNano()
	fc.lockHolder = token
	return token, true
}

func (fc *fakeCluster) release(token int64) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lockHolder == token {
		fc.lockHolder = 0
	}
}

// lockFunc mirrors lockFailureDomainTransitions: retry until the transition
// timeout, then give up with ErrFailureDomainTransitionBusy.
func (fc *fakeCluster) lockFunc(ctx context.Context, _ mcTypes.State) (int64, error) {
	deadline := time.Now().Add(failureDomainTransitionTimeout)
	for {
		token, ok := fc.tryAcquire()
		if ok {
			return token, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("%w: lock still held after %s", ErrFailureDomainTransitionBusy, failureDomainTransitionTimeout)
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("%w: cancelled while waiting: %v", ErrFailureDomainTransitionBusy, ctx.Err())
		case <-time.After(failureDomainLockRetrySleep):
		}
	}
}

type fakeClusterSnapshot struct {
	defaultRl string
	pools     map[string]string
	denied    int
}

func (fc *fakeCluster) snap() fakeClusterSnapshot {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	pools := map[string]string{}
	for pool, rule := range fc.pools {
		pools[pool] = rule
	}
	return fakeClusterSnapshot{defaultRl: fc.defaultRl, pools: pools, denied: fc.denied}
}

// armPause suspends the next pool listing — the pool list capture inside the
// host switch — until resumePause is called.
func (fc *fakeCluster) armPause() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.pauseArmed = true
}

func (fc *fakeCluster) resumePause() {
	close(fc.resume)
}

// exec mutates/reads the fake cluster state, mirroring the ceph commands the
// failure domain paths issue.
func (fc *fakeCluster) exec(name string, args []string) (string, error) {
	if name != "ceph" {
		return "", fmt.Errorf("unexpected command %s %v", name, args)
	}
	joined := strings.Join(args, " ")

	switch {
	case joined == "config get mon osd_pool_default_crush_rule":
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return fmt.Sprintf("%d\n", fc.ruleIDs[fc.defaultRl]), nil

	case joined == "osd crush rule ls":
		return "microceph_auto_osd\nmicroceph_auto_host\nmicroceph_auto_rack", nil

	case strings.HasPrefix(joined, "osd crush rule dump "):
		rule := args[len(args)-1]
		fc.mu.Lock()
		defer fc.mu.Unlock()
		id, ok := fc.ruleIDs[rule]
		if !ok {
			return "", fmt.Errorf("no such rule %s", rule)
		}
		return fmt.Sprintf(`{"rule_id": %d}`, id), nil

	case strings.HasPrefix(joined, "osd crush rule create-replicated "):
		fc.mu.Lock()
		defer fc.mu.Unlock()
		rule := args[4]
		if _, ok := fc.ruleIDs[rule]; ok {
			return "", nil
		}
		fc.ruleIDs[rule] = fc.nextRuleID
		fc.nextRuleID++
		return "", nil

	case strings.HasPrefix(joined, "config set global osd_pool_default_crush_rule "):
		// args: config set global osd_pool_default_crush_rule <id> -f json-pretty
		fc.mu.Lock()
		defer fc.mu.Unlock()
		id, err := strconv.Atoi(args[4])
		if err != nil {
			return "", err
		}
		for ruleName, ruleID := range fc.ruleIDs {
			if ruleID == id {
				fc.defaultRl = ruleName
				return "", nil
			}
		}
		return "", fmt.Errorf("no rule with id %d", id)

	case joined == "osd pool ls detail --format=json":
		// Build the response without holding the mutex past the pause point:
		// a suspended holder must not block the other node's lock attempts.
		fc.mu.Lock()
		armed := fc.pauseArmed
		fc.pauseArmed = false
		type poolEntry struct {
			PoolName  string `json:"pool_name"`
			CrushRule int    `json:"crush_rule"`
		}
		out := []poolEntry{}
		for pool, rule := range fc.pools {
			out = append(out, poolEntry{PoolName: pool, CrushRule: fc.ruleIDs[rule]})
		}
		fc.mu.Unlock()

		if armed {
			fc.pauseHit <- struct{}{}
			<-fc.resume
		}
		b, err := json.Marshal(out)
		return string(b), err

	case strings.HasPrefix(joined, "osd pool set "):
		// args: osd pool set <pool> crush_rule <rule>
		fc.mu.Lock()
		defer fc.mu.Unlock()
		pool, rule := args[3], args[5]
		if _, ok := fc.pools[pool]; !ok {
			return "", fmt.Errorf("no such pool %s", pool)
		}
		if _, ok := fc.ruleIDs[rule]; !ok {
			return "", fmt.Errorf("no such rule %s", rule)
		}
		fc.pools[pool] = rule
		return "", nil

	case joined == "osd tree -f json":
		return osdTreeWithOSDs([]string{"az-a", "az-b", "az-c"}), nil

	case strings.HasPrefix(joined, "osd crush add-bucket "), strings.HasPrefix(joined, "osd crush move "):
		return "", nil
	}
	return "", fmt.Errorf("unexpected ceph command: %s", joined)
}

// raceRunner routes all ceph commands through the fake cluster.
type raceRunner struct {
	fc *fakeCluster
}

func (r *raceRunner) RunCommand(name string, args ...string) (string, error) {
	return r.fc.exec(name, args)
}

func (r *raceRunner) RunCommandContext(_ context.Context, name string, args ...string) (string, error) {
	return r.fc.exec(name, args)
}

// TestUpdateFailureDomainSerializedAgainstRackPromotion reproduces the race
// behind the serialized failure domain transitions: a host transition that
// has just captured its pool list must not revert a rack promotion another
// member completes while it is suspended. With the transition lock stubbed to
// a no-op (the pre-serialization behavior), node B's promotion runs inside
// node A's window and node A's stale switch reverts it; with serialization,
// B is denied while A holds the lock and completes cleanly afterwards.
func TestUpdateFailureDomainSerializedAgainstRackPromotion(t *testing.T) {
	fc := newFakeCluster(".mgr", "cinder-ceph")

	origExec := common.ProcessExec
	common.ProcessExec = &raceRunner{fc: fc}
	t.Cleanup(func() { common.ProcessExec = origExec })

	origLock := lockFailureDomainTransitionsFunc
	origUnlock := unlockFailureDomainTransitionsFunc
	lockFailureDomainTransitionsFunc = fc.lockFunc
	unlockFailureDomainTransitionsFunc = func(_ context.Context, _ mcTypes.State, token int64) error {
		fc.release(token)
		return nil
	}
	t.Cleanup(func() {
		lockFailureDomainTransitionsFunc = origLock
		unlockFailureDomainTransitionsFunc = origUnlock
	})

	// Short lock waits keep a failing run quick; the test itself is synchronous.
	origTimeout := failureDomainTransitionTimeout
	origSleep := failureDomainLockRetrySleep
	failureDomainTransitionTimeout = 50 * time.Millisecond
	failureDomainLockRetrySleep = time.Millisecond
	t.Cleanup(func() {
		failureDomainTransitionTimeout = origTimeout
		failureDomainLockRetrySleep = origSleep
	})

	origCounter := database.MemberCounter
	c := mocks.NewMemberCounterInterface(t)
	c.On("Count", mock.Anything, mock.Anything).Return(3, nil).Once()
	database.MemberCounter = c
	t.Cleanup(func() { database.MemberCounter = origCounter })

	u := api.NewURL()
	nodeA := &mocks.MockState{URL: u, ClusterName: "node-a"}
	nodeB := &mocks.MockState{URL: u, ClusterName: "node-b"}

	bAZs := azData{
		hostAZ:    "az-a",
		uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true},
	}

	// Node A decides "not on rack" and starts its host switch, then suspends
	// right after capturing the pool list, holding the transition lock.
	fc.armPause()
	nodeADone := make(chan error, 1)
	go func() {
		mgrA := NewOSDManager(nodeA)
		nodeADone <- mgrA.updateFailureDomain(context.Background(), nodeA)
	}()
	select {
	case <-fc.pauseHit:
	case err := <-nodeADone:
		t.Fatalf("node A finished before reaching the pool capture point: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for node A to reach the pool capture point")
	}

	// Node B attempts a rack promotion while node A is suspended mid-switch.
	mgrB := NewOSDManager(nodeB)
	errB := mgrB.promoteToRackFailureDomain(context.Background(), bAZs)
	assert.NoError(t, errB)

	mid := fc.snap()
	assert.NotZero(t, mid.denied,
		"rack promotion must be denied while the host transition holds the lock")
	assert.Equal(t, "microceph_auto_host", mid.defaultRl,
		"rack promotion must not run while the host transition holds the lock")

	// Node A finishes its (now stale) host switch and releases the lock.
	fc.resumePause()
	assert.NoError(t, <-nodeADone)

	// Node B retries the promotion and, with the lock free, completes it.
	errB = mgrB.promoteToRackFailureDomain(context.Background(), bAZs)
	assert.NoError(t, errB)

	final := fc.snap()
	assert.Equal(t, "microceph_auto_rack", final.defaultRl,
		"the stale host transition must not win; the cluster must end on rack")
	for pool, rule := range final.pools {
		assert.Equal(t, "microceph_auto_rack", rule, "pool %s must end on the rack rule", pool)
	}
}
