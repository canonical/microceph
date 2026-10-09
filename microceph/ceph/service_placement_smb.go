package ceph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var smbRADOSURIRegex = regexp.MustCompile(`^rados://\.smb/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)
var smbPostPlacementCheckFunc = func(ctx context.Context, service string) error {
	return waitForSMBServiceReady(ctx, service, time.Second)
}

type smbCTDBPlacement struct {
	Rank     int    `json:"rank"`
	Identity string `json:"identity"`
}

type smbBindAddress struct {
	Address string `json:"address"`
	Network string `json:"network"`
}

// SMBServicePlacement describes the node-local configuration of an SMB service.
type SMBServicePlacement struct {
	ClusterID      string           `json:"cluster_id"`
	ConfigURI      string           `json:"config_uri"`
	Features       []string         `json:"features"`
	JoinSources    []string         `json:"join_sources"`
	UserSources    []string         `json:"user_sources"`
	ClusterMetaURI string           `json:"cluster_meta_uri"`
	ClusterLockURI string           `json:"cluster_lock_uri"`
	BindAddrs      []smbBindAddress `json:"bind_addrs"`
	CustomPorts    map[string]int   `json:"custom_ports"`

	ctdb              *smbCTDBPlacement
	ctdbRanks         map[string]int
	nextCTDBRank      int
	bindAddress       string
	ctdbAddress       string
	upstreamSpec      json.RawMessage
	configDigest      string
	configData        *smbSourceData
	unchanged         bool
	previous          *smbLocalSnapshot
	placementCtx      context.Context
	freshPlacement    bool
	startedServices   []string
	recordedClustered *bool
}

// PopulateParams validates an SMB placement payload before it is applied locally.
func (smb *SMBServicePlacement) PopulateParams(_ interfaces.StateInterface, payload string) error {
	err := smb.decodePayload(payload)
	if err != nil {
		return err
	}

	if !types.SMBClusterIDRegex.MatchString(smb.ClusterID) {
		return fmt.Errorf("expected cluster_id to be valid (regex: '%s')", types.SMBClusterIDRegex.String())
	}

	err = validateSMBFeatures(smb.Features)
	if err != nil {
		return err
	}

	if smb.isClustered() {
		err = smb.validateCTDB()
		if err != nil {
			return err
		}
	} else if smb.ctdb != nil || len(smb.ctdbRanks) > 0 || smb.nextCTDBRank != 0 || smb.ClusterMetaURI != "" || smb.ClusterLockURI != "" {
		return fmt.Errorf("CTDB metadata requires the clustered SMB feature")
	}

	if len(smb.JoinSources) > 0 {
		return fmt.Errorf("direct SMB service does not support domain join sources")
	}

	err = smb.validateNetworkOptions()
	if err != nil {
		return err
	}

	err = validateSMBConfigURI(smb.ClusterID, smb.ConfigURI)
	if err != nil {
		return err
	}

	for _, source := range smb.UserSources {
		err = validateSMBUserSourceURI(smb.ClusterID, source)
		if err != nil {
			return err
		}
	}

	return nil
}

func (smb *SMBServicePlacement) decodePayload(payload string) error {
	payloadData := []byte(payload)
	upstreamSpec := payloadData
	data := payloadData

	var transport struct {
		ServiceSpec json.RawMessage `json:"service_spec"`
		MicroCeph   struct {
			CTDB         *smbCTDBPlacement `json:"ctdb"`
			CTDBRanks    map[string]int    `json:"ctdb_ranks"`
			NextCTDBRank int               `json:"next_ctdb_rank"`
		} `json:"microceph"`
	}
	err := json.Unmarshal(payloadData, &transport)
	if err != nil {
		return fmt.Errorf("failed to decode SMB service payload: %w", err)
	}
	if len(transport.ServiceSpec) > 0 && string(transport.ServiceSpec) != "null" {
		upstreamSpec = transport.ServiceSpec
		data = transport.ServiceSpec
	}

	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	err = json.Unmarshal(data, &envelope)
	if err != nil {
		return fmt.Errorf("failed to decode SMB service payload: %w", err)
	}
	if len(envelope.Spec) > 0 && string(envelope.Spec) != "null" {
		data = envelope.Spec
	}

	var decoded SMBServicePlacement
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		return fmt.Errorf("failed to decode SMB service payload: %w", err)
	}

	decoded.ctdb = transport.MicroCeph.CTDB
	decoded.ctdbRanks = transport.MicroCeph.CTDBRanks
	decoded.nextCTDBRank = transport.MicroCeph.NextCTDBRank
	decoded.upstreamSpec = append(decoded.upstreamSpec, upstreamSpec...)
	*smb = decoded
	return nil
}

func (smb *SMBServicePlacement) upstreamSpecJSON() []byte {
	return append([]byte(nil), smb.upstreamSpec...)
}

func resolveSMBBindAddress(ctx context.Context, s interfaces.StateInterface, smb *SMBServicePlacement) (string, error) {
	if len(smb.BindAddrs) > 0 {
		var lastErr error
		for _, bind := range smb.BindAddrs {
			if bind.Address != "" {
				_, err := common.Network.FindNetworkAddress(bind.Address)
				if err == nil {
					return bind.Address, nil
				}
				lastErr = err
				continue
			}
			if bind.Network != "" {
				address, err := common.Network.FindIpOnSubnet(bind.Network)
				if err == nil {
					return address, nil
				}
				lastErr = err
			}
		}
		return "", fmt.Errorf("failed to resolve an SMB bind address: %w", lastErr)
	}

	config, err := fetchConfigDb(ctx, s)
	if err != nil {
		return "", fmt.Errorf("failed to read public network configuration: %w", err)
	}
	publicNetwork := config["public_network"]
	if publicNetwork == "" {
		return "", fmt.Errorf("public_network is not configured")
	}
	address, err := common.Network.FindIpOnSubnet(publicNetwork)
	if err != nil {
		return "", fmt.Errorf("failed to resolve an address on public_network %s: %w", publicNetwork, err)
	}
	return address, nil
}

func resolveSMBCTDBAddress(s interfaces.StateInterface) (string, error) {
	if s == nil {
		return "", fmt.Errorf("MicroCluster address is unavailable")
	}
	state := s.ClusterState()
	if state == nil || state.Address() == nil {
		return "", fmt.Errorf("MicroCluster address is unavailable")
	}
	address := state.Address().Hostname()
	if address == "" {
		return "", fmt.Errorf("MicroCluster address is empty")
	}
	return address, nil
}

// HospitalityCheck verifies that the SMB service can run with the required identity-switching permission.
func (smb *SMBServicePlacement) HospitalityCheck(_ interfaces.StateInterface) error {
	if !smbInterfaceConnected("smb-identity") {
		return fmt.Errorf("SMB service requires the smb-identity interface connection")
	}
	if smb.isClustered() && !smbInterfaceConnected("ctdb-run") {
		return fmt.Errorf("clustered SMB requires the ctdb-run interface connection")
	}

	clusterID, err := currentSMBClusterID()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if clusterID != smb.ClusterID {
		return fmt.Errorf("SMB service already manages cluster '%s' on this host", clusterID)
	}
	return smb.checkClusteringMode()
}

// ServiceInit materializes the current SMB configuration and starts or restarts its services.
func (smb *SMBServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	smb.placementCtx = ctx
	if s != nil {
		// MicroCluster transactions require the logger in the supplied context.
		// Bound the preflight and reject mode changes before local mutation.
		preflightCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		state := s.ClusterState()
		err := state.Database().Transaction(preflightCtx, func(ctx context.Context, tx *sql.Tx) error {
			err := database.CheckSMBPlacementMode(ctx, tx, smb.ClusterID, state.Name(), smb.isClustered())
			if err != nil {
				return err
			}
			smb.recordedClustered, err = database.GetSMBPlacementMode(ctx, tx, smb.ClusterID)
			if err != nil {
				return err
			}
			if !smb.isClustered() {
				return nil
			}
			config, rank, err := database.ReserveSMBCTDBRank(ctx, tx, smb.ClusterID, smb.upstreamSpecJSON(), smb.ctdb.Identity)
			if err != nil {
				return err
			}
			smb.ctdb.Rank = rank
			smb.ctdbRanks = config.CTDBRanks
			smb.nextCTDBRank = config.NextCTDBRank
			return nil
		})
		cancel()
		if err != nil {
			return err
		}
	}
	clusterID, stateErr := currentSMBClusterID()
	freshPlacement := os.IsNotExist(stateErr)
	if stateErr != nil && !freshPlacement {
		return stateErr
	}
	if !freshPlacement && clusterID != smb.ClusterID {
		return fmt.Errorf("SMB service already manages cluster '%s' on this host", clusterID)
	}
	err := smb.checkClusteringMode()
	if err != nil {
		return err
	}
	smb.freshPlacement = freshPlacement
	cleanupFreshFailure := smb.rollbackPlacement

	bindAddress, err := resolveSMBBindAddress(ctx, s, smb)
	if err != nil {
		return cleanupFreshFailure(err)
	}
	smb.bindAddress = bindAddress
	if smb.isClustered() {
		smb.ctdbAddress, err = resolveSMBCTDBAddress(s)
		if err != nil {
			return cleanupFreshFailure(err)
		}
	}
	// Fetch on every PUT: mgr/smb updates shares and users at stable URIs.
	smb.configData, err = fetchSMBConfigSources(ctx, smb)
	if err != nil {
		return cleanupFreshFailure(err)
	}
	smb.configDigest, err = smb.effectiveConfigDigest()
	if err != nil {
		return cleanupFreshFailure(err)
	}
	if !freshPlacement && s != nil {
		smb.unchanged, err = smb.matchesAppliedReceipt(ctx, s)
		if err != nil {
			return err
		}
		if smb.unchanged {
			return nil
		}
	}

	if !freshPlacement {
		smb.previous, err = snapshotSMBLocalState(smb.ClusterID)
		if err != nil {
			return fmt.Errorf("failed to snapshot SMB configuration before update: %w", err)
		}
	}
	err = materializeSMBConfig(ctx, smb)
	if err != nil {
		return cleanupFreshFailure(err)
	}

	err = writeSMBDataKeyring(ctx, smb.ClusterID)
	if err != nil {
		return cleanupFreshFailure(err)
	}

	services := []string{}
	if smb.isClustered() {
		err = writeSMBCTDBAddress(smb.ctdbAddress, bindAddress, smb.CustomPorts["smb"])
		if err != nil {
			return cleanupFreshFailure(err)
		}
		err = writeSMBConfigKeyring(ctx, smb.ClusterID)
		if err != nil {
			return cleanupFreshFailure(err)
		}
		services = append(services, "ctdbd", "ctdb-nodes")
	}
	services = append(services, "smbd")

	for _, service := range services {
		// Even a failed/timed-out start may have enabled its daemon in snapd.
		smb.startedServices = append(smb.startedServices, service)
		_, err = startOrRestartSMBService(ctx, service)
		if err != nil {
			return cleanupFreshFailure(err)
		}
	}
	return nil
}

func startOrRestartSMBServices(ctx context.Context, services []string) error {
	started := []string{}
	for _, service := range services {
		newlyStarted, err := startOrRestartSMBService(ctx, service)
		if err == nil {
			if newlyStarted {
				started = append(started, service)
			}
			continue
		}

		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), smbCleanupTimeout)
		defer cancel()
		for index := len(started) - 1; index >= 0; index-- {
			rollbackErr := smbSnapAction(cleanupCtx, "stop", started[index], true)
			if rollbackErr != nil {
				return fmt.Errorf(
					"%w; failed to roll back SMB service %s: %v",
					err,
					started[index],
					rollbackErr,
				)
			}
		}
		return err
	}
	return nil
}

func startOrRestartSMBService(ctx context.Context, service string) (bool, error) {
	err := smbSnapCheckActive(ctx, service)
	if err != nil {
		err = smbSnapAction(ctx, "start", service, true)
		if err != nil {
			return false, fmt.Errorf("failed to start SMB service %s: %w", service, err)
		}
		return true, nil
	}

	err = smbSnapAction(ctx, "restart", service, false)
	if err != nil {
		return false, fmt.Errorf("failed to restart SMB service %s: %w", service, err)
	}
	return false, nil
}

// PostPlacementCheck verifies local daemon readiness before recording placement.
func (smb *SMBServicePlacement) PostPlacementCheck(_ interfaces.StateInterface) error {
	ctx := smb.placementCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, smbReadinessTimeout)
	defer cancel()
	for _, service := range smb.requiredServices() {
		err := smbPostPlacementCheckFunc(ctx, service)
		if err != nil {
			return smb.rollbackPlacement(err)
		}
	}
	if smb.isClustered() {
		if smb.ctdb == nil {
			return smb.rollbackPlacement(fmt.Errorf("missing assigned CTDB PNN"))
		}
		err := checkSMBCTDBPNN(ctx, smb.ctdb.Rank)
		if err != nil {
			return smb.rollbackPlacement(err)
		}
	}
	return nil
}

// DbUpdate records the successful SMB configuration and local member state.
func (smb *SMBServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	// No daemon restart does not mean no group change: count-based placement
	// may reserve ranks for new members while this member's config is unchanged.
	groupConfig := database.SMBServiceGroupConfig{
		DesiredSpec:  smb.upstreamSpecJSON(),
		CTDBRanks:    smb.ctdbRanks,
		NextCTDBRank: smb.nextCTDBRank,
	}
	serviceInfo := database.SMBServiceInfo{
		ConfigURI:    smb.ConfigURI,
		AppliedSpec:  smb.upstreamSpecJSON(),
		ConfigDigest: smb.configDigest,
	}
	if smb.ctdb != nil {
		rank := smb.ctdb.Rank
		serviceInfo.CTDBRank = &rank
		serviceInfo.CTDBIdentity = smb.ctdb.Identity
	}
	err := database.GroupedServicesQuery.AddOrUpdate(ctx, s, "smb", smb.ClusterID, groupConfig, serviceInfo)
	if err != nil {
		return smb.rollbackPlacement(err)
	}
	smb.previous = nil
	smb.freshPlacement = false
	smb.startedServices = nil
	return nil
}

func (smb *SMBServicePlacement) effectiveConfigDigest() (string, error) {
	var spec map[string]json.RawMessage
	if len(smb.upstreamSpec) > 0 {
		err := json.Unmarshal(smb.upstreamSpec, &spec)
		if err != nil {
			return "", fmt.Errorf("failed to decode SMB configuration for hashing: %w", err)
		}
	}
	// Scheduling-only placement is not part of the node-local configuration.
	// Keep the complete upstream spec unchanged for group and receipt updates.
	delete(spec, "placement")
	data, err := json.Marshal(struct {
		Spec        map[string]json.RawMessage `json:"spec"`
		CTDB        *smbCTDBPlacement          `json:"ctdb"`
		BindAddress string                     `json:"bind_address"`
		CTDBAddress string                     `json:"ctdb_address"`
		Container   []byte                     `json:"container"`
		Users       [][]byte                   `json:"users"`
	}{spec, smb.ctdb, smb.bindAddress, smb.ctdbAddress, smb.configData.container, smb.configData.users})
	if err != nil {
		return "", fmt.Errorf("failed to hash SMB configuration: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (smb *SMBServicePlacement) matchesAppliedReceipt(ctx context.Context, s interfaces.StateInterface) (bool, error) {
	services, err := database.GroupedServicesQuery.GetGroupedServicesOnHost(ctx, s)
	if err != nil {
		return false, fmt.Errorf("failed to read SMB placement receipt: %w", err)
	}
	for _, service := range services {
		if service.Service != "smb" || service.GroupID != smb.ClusterID {
			continue
		}
		var receipt database.SMBServiceInfo
		err = json.Unmarshal([]byte(service.Info), &receipt)
		if err != nil || len(receipt.AppliedSpec) == 0 || receipt.ConfigDigest == "" || receipt.ConfigDigest != smb.configDigest {
			return false, nil
		}
		if smb.ctdb != nil && (receipt.CTDBRank == nil || *receipt.CTDBRank != smb.ctdb.Rank || receipt.CTDBIdentity != smb.ctdb.Identity) {
			return false, nil
		}
		if smb.ctdb == nil && (receipt.CTDBRank != nil || receipt.CTDBIdentity != "") {
			return false, nil
		}
		if !smb.hasLocalConfig() {
			return false, nil
		}
		for _, name := range smb.requiredServices() {
			if smbSnapCheckActive(ctx, name) != nil {
				return false, nil
			}
		}
		return true, nil
	}
	return false, nil
}

func (smb *SMBServicePlacement) hasLocalConfig() bool {
	paths := constants.GetPathConst()
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")
	files := []string{
		filepath.Join(paths.ConfPath, "samba", "smb.conf"),
		filepath.Join(runtimeDir, "container.json"),
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.fs.cluster.%s.keyring", smb.ClusterID)),
	}
	if smb.isClustered() {
		for _, name := range []string{"ctdb.json", "ctdb-rank", "ctdb-identity", "ctdb-address"} {
			files = append(files, filepath.Join(runtimeDir, name))
		}
		files = append(files, filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.config.%s.keyring", smb.ClusterID)))
		files = append(files, filepath.Join(paths.DataPath, "samba", "smb.ctdb.conf"))
	}
	for _, path := range files {
		_, err := os.Stat(path)
		if err != nil {
			return false
		}
	}
	if smb.configData == nil {
		return false
	}
	container, err := os.ReadFile(filepath.Join(runtimeDir, "container.json"))
	if err != nil || !bytes.Equal(container, smb.configData.container) {
		return false
	}
	for index, user := range smb.configData.users {
		local, err := os.ReadFile(filepath.Join(runtimeDir, fmt.Sprintf("users-%d.json", index)))
		if err != nil || !bytes.Equal(local, user) {
			return false
		}
	}
	users, err := filepath.Glob(filepath.Join(runtimeDir, "users-*.json"))
	if err != nil || len(users) != len(smb.configData.users) {
		return false
	}
	return true
}

func (smb *SMBServicePlacement) rollbackPlacement(cause error) error {
	ctx := smb.placementCtx
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), smbCleanupTimeout)
	defer cancel()
	if smb.freshPlacement {
		for index := len(smb.startedServices) - 1; index >= 0; index-- {
			service := smb.startedServices[index]
			err := smbSnapAction(cleanupCtx, "stop", service, true)
			if err != nil {
				return fmt.Errorf("%w; failed to stop incomplete SMB service %s (configuration retained): %v", cause, service, err)
			}
		}
		if smb.isClustered() && len(smb.startedServices) > 0 {
			err := retireSMBCTDBMemberFunc(cleanupCtx, smb.ClusterID)
			if err != nil {
				return fmt.Errorf("%w; CTDB cleanup failed (configuration retained): %v", cause, err)
			}
		}
		err := removeSMBLocalState(smb.ClusterID)
		if err != nil {
			return fmt.Errorf("%w; failed to clean up incomplete SMB initialization: %v", cause, err)
		}
		smb.startedServices = nil
		return cause
	}
	if smb.previous == nil {
		return cause
	}
	snapshot := smb.previous
	smb.previous = nil
	err := snapshot.restore()
	if err == nil {
		err = startOrRestartSMBServices(cleanupCtx, smb.requiredServices())
	}
	if err != nil {
		return fmt.Errorf("%w; failed to restore previous SMB service: %v", cause, err)
	}
	return cause
}

func (smb *SMBServicePlacement) requiredServices() []string {
	if smb.isClustered() {
		return []string{"ctdbd", "ctdb-nodes", "smbd"}
	}
	return []string{"smbd"}
}

func currentSMBClusterID() (string, error) {
	paths := constants.GetPathConst()
	path := filepath.Join(filepath.Dir(paths.ConfPath), "samba", "cluster-id")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	clusterID := strings.TrimSpace(string(data))
	if clusterID == "" {
		return "", fmt.Errorf("SMB cluster ID file is empty")
	}

	return clusterID, nil
}

func writeSMBCTDBAddress(address string, bindAddress string, smbPort int) error {
	paths := constants.GetPathConst()
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")
	err := writeSMBFileAtomic(
		filepath.Join(runtimeDir, "ctdb-address"),
		[]byte(address+"\n"),
		constants.PermissionOnlyUserAccess,
	)
	if err != nil {
		return fmt.Errorf("failed to write CTDB address: %w", err)
	}

	ctdbSMBConfigPath := filepath.Join(paths.DataPath, "samba", "smb.ctdb.conf")
	err = os.MkdirAll(filepath.Dir(ctdbSMBConfigPath), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to create SMB data directory: %w", err)
	}
	config := fmt.Sprintf("[global]\nctdbd socket = /run/ctdb/ctdbd.socket\nbind interfaces only = yes\ninterfaces = %s\n", bindAddress)
	if smbPort != 0 {
		config += fmt.Sprintf("smb ports = %d\n", smbPort)
	}
	err = writeSMBFileAtomic(
		ctdbSMBConfigPath,
		[]byte(config),
		constants.PermissionUserRwWorldRAccess,
	)
	if err != nil {
		return fmt.Errorf("failed to write CTDB Samba configuration: %w", err)
	}
	return nil
}

func writeSMBDataKeyring(ctx context.Context, clusterID string) error {
	entity := fmt.Sprintf("client.smb.fs.cluster.%s", clusterID)
	keyring, err := runSMBCommand(ctx, "ceph", "auth", "get", entity)
	if err != nil {
		return fmt.Errorf("failed to fetch SMB CephX keyring: %w", err)
	}

	paths := constants.GetPathConst()
	path := filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.%s.keyring", entity))
	err = writeSMBFileAtomic(path, []byte(keyring), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write SMB CephX keyring: %w", err)
	}

	return nil
}

func writeSMBConfigKeyring(ctx context.Context, clusterID string) error {
	entity := fmt.Sprintf("client.smb.config.%s", clusterID)
	osdCaps := fmt.Sprintf(
		"allow rwx pool=.smb namespace=%s object_prefix cluster.meta.",
		clusterID,
	)
	keyring, err := runSMBCommand(ctx, "ceph",
		"auth", "get-or-create", entity,
		"mon", "allow r",
		"osd", osdCaps,
	)
	if err != nil {
		return fmt.Errorf("failed to create SMB configuration keyring: %w", err)
	}

	paths := constants.GetPathConst()
	path := filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.%s.keyring", entity))
	err = writeSMBFileAtomic(path, []byte(keyring), constants.PermissionOnlyUserAccess)
	if err != nil {
		return fmt.Errorf("failed to write SMB configuration keyring: %w", err)
	}
	return nil
}

func (smb *SMBServicePlacement) validateNetworkOptions() error {
	for _, bind := range smb.BindAddrs {
		if (bind.Address == "") == (bind.Network == "") {
			return fmt.Errorf("SMB bind address must set exactly one of address or network")
		}
	}
	for name, port := range smb.CustomPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("SMB custom port %s is invalid", name)
		}
		switch name {
		case "smb":
		case "ctdb":
			if port != 4379 {
				return fmt.Errorf("direct SMB service does not support a custom CTDB port")
			}
		default:
			return fmt.Errorf("direct SMB service does not support custom port '%s'", name)
		}
	}
	return nil
}

func validateSMBFeatures(features []string) error {
	for _, feature := range features {
		if feature != "clustered" {
			return fmt.Errorf("direct SMB service does not support SMB feature '%s'", feature)
		}
	}

	return nil
}

func (smb *SMBServicePlacement) checkClusteringMode() error {
	if smb.recordedClustered != nil && *smb.recordedClustered != smb.isClustered() {
		return fmt.Errorf("SMB clustering mode cannot be changed; recreate the cluster")
	}
	return nil
}

func (smb *SMBServicePlacement) isClustered() bool {
	for _, feature := range smb.Features {
		if feature == "clustered" {
			return true
		}
	}
	return false
}

func (smb *SMBServicePlacement) validateCTDB() error {
	if smb.ctdb == nil {
		return fmt.Errorf("clustered SMB requires CTDB node metadata")
	}
	if smb.ctdb.Rank < 0 {
		return fmt.Errorf("CTDB rank must not be negative")
	}
	if smb.ctdb.Identity == "" {
		return fmt.Errorf("clustered SMB requires a CTDB node identity")
	}

	err := validateSMBClusterURI(smb.ClusterID, smb.ClusterMetaURI, "cluster.meta.json")
	if err != nil {
		return fmt.Errorf("invalid cluster metadata URI: %w", err)
	}
	err = validateSMBClusterURI(smb.ClusterID, smb.ClusterLockURI, "cluster.meta.lock")
	if err != nil {
		return fmt.Errorf("invalid cluster lock URI: %w", err)
	}
	return nil
}

func validateSMBClusterURI(clusterID string, uri string, expectedObject string) error {
	matches := smbRADOSURIRegex.FindStringSubmatch(uri)
	if matches == nil {
		return fmt.Errorf("expected a .smb RADOS URI")
	}
	if matches[1] != clusterID {
		return fmt.Errorf("URI must use SMB cluster namespace '%s'", clusterID)
	}
	if matches[2] != expectedObject {
		return fmt.Errorf("URI must name %s", expectedObject)
	}
	return nil
}

func validateSMBConfigURI(clusterID string, configURI string) error {
	matches := smbRADOSURIRegex.FindStringSubmatch(configURI)
	if matches == nil {
		return fmt.Errorf("expected config_uri to be a .smb RADOS URI")
	}

	namespace := matches[1]
	object := matches[2]
	if namespace != clusterID {
		return fmt.Errorf("config_uri must use SMB cluster namespace '%s'", clusterID)
	}

	if object != "config.smb" {
		return fmt.Errorf("config_uri must name the config.smb object")
	}

	return nil
}

func validateSMBUserSourceURI(clusterID string, source string) error {
	prefix := fmt.Sprintf("rados:mon-config-key:smb/config/%s/", clusterID)
	if !strings.HasPrefix(source, prefix) {
		return fmt.Errorf("user source must use the SMB config-key prefix for cluster '%s'", clusterID)
	}

	name := strings.TrimPrefix(source, prefix)
	if name == "" || !strings.HasSuffix(name, ".json") {
		return fmt.Errorf("user source must name an SMB JSON config key")
	}

	return nil
}

func validateSMBContainerConfig(data []byte) error {
	var config struct {
		Shares map[string]struct {
			Options map[string]any `json:"options"`
		} `json:"shares"`
	}

	err := json.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("failed to decode SMB container configuration: %w", err)
	}

	for shareName, share := range config.Shares {
		vfsObjects, ok := share.Options["vfs objects"].(string)
		if !ok || !containsVFSObject(vfsObjects, "ceph_new") {
			return fmt.Errorf("share '%s' must use direct samba-vfs/new", shareName)
		}

		proxy, ok := share.Options["ceph_new:proxy"].(string)
		if !ok || !strings.EqualFold(proxy, "no") {
			return fmt.Errorf("share '%s' must use direct samba-vfs/new", shareName)
		}
	}

	return nil
}

func containsVFSObject(vfsObjects string, object string) bool {
	for _, current := range strings.Fields(vfsObjects) {
		if current == object {
			return true
		}
	}

	return false
}
