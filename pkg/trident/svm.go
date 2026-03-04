package trident

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"

	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	"github.com/metal-stack/ontap-go/api/client/networking"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
	"github.com/metal-stack/ontap-go/api/client/storage"
	"github.com/metal-stack/ontap-go/api/models"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
)

var (
	// ErrSvmNotFound is returned if the svm was not found
	ErrSvmNotFound = errors.New("NotFound")
	// ErrAlreadyExists is returned when the entity already exists
	ErrAlreadyExists = errors.New("AlreadyExists")

	ErrSeedSecretMissing = errors.New("SeedSecretMissing")
)

// NetworkTags for SVM network interfaces
const (
	// dataLifTag is the tag used to identify data network interfaces
	dataLifTag = "datalif"

	// managementLifTag is the tag used to identify management network interfaces
	managementLifTag = "managementlif"
)

// CreateSVMOptions holds the parameters required for CreateSVM function.
type CreateSVMOptions struct {
	ProjectID              string
	ShootNamespace         string // Full namespace like "shoot--<project>--<name>"
	SvmIpaddresses         ontapv1alpha1.SvmIpaddresses
	SvmSeedSecretNamespace string
}

// networkInterfaceOptions holds the parameters required for createNetworkInterfaceForSvm function.
type networkInterfaceOptions struct {
	svmUUID   string
	svmName   string
	ipAddress string
	lifName   string
	nodeUUID  string
	isDataLif bool
}

type SvmManager struct {
	log        logr.Logger
	clients    []*ontapv1.Ontap
	seedClient client.Client
}

func NewSvmManager(log logr.Logger, clients []*ontapv1.Ontap, seedClient client.Client) *SvmManager {
	return &SvmManager{
		log:        log,
		clients:    clients,
		seedClient: seedClient,
	}
}

// getWriteClient dynamically selects the client with the fewest total volumes.
func (m *SvmManager) getWriteClient(ctx context.Context) (*ontapv1.Ontap, error) {
	var (
		bestClient      *ontapv1.Ontap
		bestClientIndex int   = -1
		minVolumes      int64 = math.MaxInt64
	)

	for i, c := range m.clients {
		params := storage.NewAggregateCollectionGetParamsWithContext(ctx)
		params.Fields = []string{"volume-count"}

		result, err := c.Storage.AggregateCollectionGet(params, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get aggregate volume counts: %w", err)
		}

		var volumeCount int64
		var nilCount int
		for _, aggr := range result.Payload.AggregateResponseInlineRecords {
			if aggr.VolumeCount == nil {
				nilCount++
				m.log.Info("aggregate with nil volume_count", "client_index", i, "aggregate", aggr.Name, "uuid", aggr.UUID)
				continue
			}
			m.log.Info("aggregate volume_count", "client_index", i, "aggregate", aggr.Name, "volume_count", *aggr.VolumeCount)
			volumeCount += *aggr.VolumeCount
		}
		m.log.Info("client total volume count", "client_index", i, "volume_count", volumeCount, "aggregates", len(result.Payload.AggregateResponseInlineRecords), "nil_volume_counts", nilCount)

		if volumeCount < minVolumes {
			minVolumes = volumeCount
			bestClient = c
			bestClientIndex = i
		}
	}

	if bestClient == nil {
		return nil, fmt.Errorf("no suitable write client found")
	}

	m.log.Info("selected write client", "client_index", bestClientIndex, "volume_count", minVolumes)
	return bestClient, nil
}

// CreateSVM creates an SVM and sets up network interfaces on a selected node
func (m *SvmManager) CreateSVM(ctx context.Context, opts CreateSVMOptions) error {
	m.log.Info("Creating SVM with IPs", "name", opts.ProjectID, "managementLif", opts.SvmIpaddresses.ManagementLif, "dataLifs", opts.SvmIpaddresses.DataLifs)

	// 0. Dynamically select the write target based on volume counts
	writeClient, err := m.getWriteClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to select write client: %w", err)
	}

	// 1. Get a node for network interface placement and aggregate selection
	nodesUUIDs, err := m.getAllNodesInCluster(ctx, writeClient)
	if err != nil {
		return fmt.Errorf("failed to get a node for SVM creation: %w", err)
	}

	// 2. Trident needs a svm assigned, but is not exclusive to that aggregate
	// Ontap uses all aggregates per default to assign aggregates.
	agrgp := storage.NewAggregateCollectionGetParamsWithContext(ctx)
	// Got these from the Swagger UI, i don't think there is another way
	agrcget, err := writeClient.Storage.AggregateCollectionGet(agrgp, nil)
	if err != nil {
		return err
	}
	// Use all aggregates
	aggragetRecord := agrcget.Payload.AggregateResponseInlineRecords
	var aggrArrayItem []*models.SvmInlineAggregatesInlineArrayItem

	m.log.Info("aggr collection get return", "agrcg", agrcget.Payload)

	for _, aggregate := range aggragetRecord {
		aggrItem := models.SvmInlineAggregatesInlineArrayItem{
			UUID: aggregate.UUID,
		}
		aggrArrayItem = append(aggrArrayItem, &aggrItem)

	}

	m.log.Info("Assigning SVM to selected aggregate", "svm", opts.ProjectID, "aggr", aggrArrayItem)

	// 3. Create the SVM without network interfaces
	params := &s_vm.SvmCreateParams{
		Info: &models.Svm{
			Name:                &opts.ProjectID,
			SvmInlineAggregates: aggrArrayItem,
			Nvme: &models.SvmInlineNvme{
				Enabled: new(true),
				Allowed: new(true),
			},
		},
		Context: ctx,
	}

	m.log.Info("Sending SVM create request", "params", fmt.Sprintf("%+v", params))
	if _, _, err = writeClient.SVM.SvmCreate(params, nil); err != nil {
		return fmt.Errorf("failed to create SVM %s: %w", opts.ProjectID, err)
	}

	m.log.Info("SVM created successfully", "name", opts.ProjectID)
	// 3. Wait for SVM to be ready and get its UUID
	svmUUID, err := m.waitForSvmReady(ctx, opts.ProjectID)
	if err != nil {
		return fmt.Errorf("SVM '%s' was not ready: %w", opts.ProjectID, err)
	}
	m.log.Info("SVM is ready", "projectId", opts.ProjectID, "uuid", svmUUID)

	// 4. Create data LIFs
	for i, datalifIp := range opts.SvmIpaddresses.DataLifs {
		selectedNodeUUID := nodesUUIDs[i%len(nodesUUIDs)]
		dataLifOpts := networkInterfaceOptions{
			svmUUID:   svmUUID,
			svmName:   opts.ProjectID,
			ipAddress: datalifIp,
			lifName:   fmt.Sprintf("%s+%d", dataLifTag, i),
			// TODO:needs to be adjusted so ips are created distributed on both nodes, PR is open for this already
			nodeUUID:  selectedNodeUUID,
			isDataLif: true,
		}
		if err := m.createNetworkInterfaceForSvm(ctx, writeClient, dataLifOpts); err != nil {
			return fmt.Errorf("failed to create data LIF for SVM %s: %w", opts.ProjectID, err)
		}
	}

	// 6. Create management LIF
	mgmtLifOpts := networkInterfaceOptions{
		svmUUID:   svmUUID,
		svmName:   opts.ProjectID,
		ipAddress: opts.SvmIpaddresses.ManagementLif,
		lifName:   managementLifTag,
		nodeUUID:  nodesUUIDs[0],
		isDataLif: false,
	}
	if err := m.createNetworkInterfaceForSvm(ctx, writeClient, mgmtLifOpts); err != nil {
		return fmt.Errorf("failed to create management LIF for SVM %s: %w", opts.ProjectID, err)
	}

	// 7. Create user and secret in svmSeedSecretNamespace namespace
	m.log.Info("Proceeding to create user and secret for SVM", "svm", opts.ProjectID, "shootNamespace", opts.ShootNamespace)
	userOpts := userAndSecretOptions{
		projectID:              opts.ProjectID,
		shootNamespace:         opts.ShootNamespace,
		svmSeedSecretNamespace: opts.SvmSeedSecretNamespace,
		seedClient:             m.seedClient,
		svmUUID:                svmUUID,
	}
	if err := m.CreateUserAndSecret(ctx, writeClient, userOpts); err != nil {
		return fmt.Errorf("SVM %s created, but failed to create user and secret: %w", opts.ProjectID, err)
	}

	m.log.Info("Successfully completed SVM creation and setup", "svm", opts.ProjectID)
	return nil
}

// getAllNodesInCluster fetches the first node name found in the ONTAP cluster
// Needs to be changed, waiting for Netapp answer
func (m *SvmManager) getAllNodesInCluster(ctx context.Context, ontapClient *ontapv1.Ontap) ([]string, error) {
	m.log.Info("Fetching first available node in cluster...")

	var nodeUUIDs []string
	params := cluster.NewNodesGetParamsWithContext(ctx)
	params.SetFields([]string{"uuid", "name"})

	result, err := ontapClient.Cluster.NodesGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nodes: %w", err)
	}
	for _, node := range result.Payload.NodeResponseInlineRecords {
		m.log.Info("Node in Response", "nodes", node)
	}

	if result.Payload == nil {
		return nil, errors.New("no node information returned")
	}

	if len(result.Payload.NodeResponseInlineRecords) == 0 {
		return nil, fmt.Errorf("nodeResponseInlineRecords is empty")
	}
	nodeRecords := result.Payload.NodeResponseInlineRecords
	for _, node := range nodeRecords {
		nodeUUIDs = append(nodeUUIDs, node.UUID.String())
	}

	if len(nodeRecords) < 2 {
		// we want more than 1 node for metro a metro cluster setup
		return nil, fmt.Errorf("less than 2 nodes were returned for cluster %v,err: %w", nodeUUIDs, err)
	}

	return nodeUUIDs, nil
}

// createNetworkInterfaceForSvm creates a network interface for the given SVM
func (m *SvmManager) createNetworkInterfaceForSvm(ctx context.Context, ontapClient *ontapv1.Ontap, opts networkInterfaceOptions) error {

	m.log.Info("Creating network interface", "svm", opts.svmName, "lifName", opts.lifName, "ip", opts.ipAddress, "node", opts.nodeUUID)

	params := networking.NewNetworkIPInterfacesCreateParamsWithContext(ctx)
	// Create the basic interface structure
	interfaceInfo := &models.IPInterface{
		Name:    new(opts.lifName),
		Enabled: new(true),
		Svm: &models.IPInterfaceInlineSvm{
			UUID: new(opts.svmUUID),
		},
	}

	paramsBgp := networking.NewNetworkIPBgpPeerGroupsGetParamsWithContext(ctx)
	bgpres, err := ontapClient.Networking.NetworkIPBgpPeerGroupsGet(paramsBgp, nil)
	if err != nil {
		return err
	}
	m.log.Info("bgp response", "bgp", bgpres)
	// A bgp neighbor is there
	// setting default netmask to 24, bc 32 is only possible if bgp peer is available and vip lif can be created
	netmask := "24"
	if bgpres.Payload.NumRecords != nil && *bgpres.Payload.NumRecords != 0 {
		netmask = "32"
		interfaceInfo.Vip = new(true)
	}

	var (
		address = new(models.IPAddress(opts.ipAddress))
		mask    = new(models.IPNetmask(netmask))
	)
	interfaceInfo.IP = &models.IPInfo{
		Address: address,
		Netmask: mask,
	}

	// Add location information
	location := &models.IPInterfaceInlineLocation{}
	location.HomeNode = &models.IPInterfaceInlineLocationInlineHomeNode{
		UUID: new(opts.nodeUUID),
	}
	interfaceInfo.Location = location
	if opts.isDataLif {
		// NVMe/TCP policy
		interfaceInfo.ServicePolicy = &models.IPInterfaceInlineServicePolicy{
			Name: new("default-data-nvme-tcp"),
		}
	}
	if !opts.isDataLif {
		// Management policy
		interfaceInfo.ServicePolicy = &models.IPInterfaceInlineServicePolicy{
			Name: new("default-management"),
		}
	}
	params.SetInfo(interfaceInfo)
	if _, err := ontapClient.Networking.NetworkIPInterfacesCreate(params, nil); err != nil {
		return fmt.Errorf("failed to create network interface %s for SVM %s: %w", opts.lifName, opts.svmName, err)
	}

	m.log.Info("Successfully created network interface", "lifName", opts.lifName, "svm", opts.svmName, "ip", opts.ipAddress)
	return nil
}

// validateAndEnsureCompleteSVMState validates all components of an SVM and creates missing parts
func (m *SvmManager) validateAndEnsureCompleteSVMState(ctx context.Context, activeClient *ontapv1.Ontap, svmUUID, svmName string, opts CreateSVMOptions) error {
	m.log.Info("Validating complete SVM state", "svmName", svmName, "uuid", svmUUID)

	// 1. Validate SVM is running and NVMe enabled
	if err := m.validateSVMRunningState(ctx, activeClient, svmUUID, svmName); err != nil {
		return err
	}

	// 2. Get cluster nodes for LIF creation (if needed)
	nodesUUIDs, err := m.getAllNodesInCluster(ctx, activeClient)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes for SVM validation: %w", err)
	}

	// 3. Validate and ensure data LIFs exist
	if err := m.validateAndEnsureDataLIFs(ctx, activeClient, svmUUID, svmName, opts.SvmIpaddresses.DataLifs, nodesUUIDs); err != nil {
		return err
	}

	// 4. Validate and ensure management LIF exists
	if err := m.validateAndEnsureManagementLIF(ctx, activeClient, svmUUID, svmName, opts.SvmIpaddresses.ManagementLif, nodesUUIDs[0]); err != nil {
		return err
	}

	userOpts := userAndSecretOptions{
		projectID:              svmName,
		shootNamespace:         opts.ShootNamespace,
		svmSeedSecretNamespace: opts.SvmSeedSecretNamespace,
		seedClient:             m.seedClient,
		svmUUID:                svmUUID,
	}
	if err := m.CreateUserAndSecret(ctx, activeClient, userOpts); err != nil {
		return fmt.Errorf("failed to ensure user and secret for SVM %s: %w", svmName, err)
	}

	m.log.Info("SVM state validation and completion successful", "svmName", svmName)
	return nil
}

// validateSVMRunningState checks if SVM is in running state with NVMe enabled
func (m *SvmManager) validateSVMRunningState(ctx context.Context, ontapClient *ontapv1.Ontap, svmUUID, svmName string) error {
	getParams := s_vm.NewSvmGetParamsWithContext(ctx)
	getParams.SetUUID(svmUUID)

	svmInfo, err := ontapClient.SVM.SvmGet(getParams, nil)
	if err != nil {
		return fmt.Errorf("failed to get SVM details for validation: %w", err)
	}
	if svmInfo.Payload == nil || svmInfo.Payload.State == nil {
		return fmt.Errorf("SVM state information is missing")
	}
	if *svmInfo.Payload.State != "running" {
		return fmt.Errorf("SVM is not in running state: %s", *svmInfo.Payload.State)
	}
	if svmInfo.Payload.Nvme == nil || svmInfo.Payload.Nvme.Enabled == nil || !*svmInfo.Payload.Nvme.Enabled {
		return fmt.Errorf("SVM NVMe is not enabled")
	}

	return nil
}

// getExistingNetworkInterfaces gets all network interfaces for an SVM
func (m *SvmManager) getExistingNetworkInterfaces(ctx context.Context, ontapClient *ontapv1.Ontap, svmUUID string) (map[string]string, error) {
	params := networking.NewNetworkIPInterfacesGetParamsWithContext(ctx)
	params.SetSvmUUID(&svmUUID)
	fields := []string{"name", "ip.address"}
	params.SetFields(fields)

	result, err := ontapClient.Networking.NetworkIPInterfacesGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %w", err)
	}

	interfaces := make(map[string]string)
	if result.Payload != nil && result.Payload.IPInterfaceResponseInlineRecords != nil {
		for _, intf := range result.Payload.IPInterfaceResponseInlineRecords {
			if intf.Name != nil && intf.IP != nil && intf.IP.Address != nil {
				interfaces[*intf.Name] = string(*intf.IP.Address)
			}
		}
	}

	return interfaces, nil
}

// validateAndEnsureDataLIFs validates all expected data LIFs exist and creates missing ones
func (m *SvmManager) validateAndEnsureDataLIFs(ctx context.Context, ontapClient *ontapv1.Ontap, svmUUID, svmName string, expectedDataLifs []string, nodesUUIDs []string) error {
	existingInterfaces, err := m.getExistingNetworkInterfaces(ctx, ontapClient, svmUUID)
	if err != nil {
		return err
	}

	for i, datalifIp := range expectedDataLifs {
		expectedLifName := fmt.Sprintf("%s+%d", dataLifTag, i)

		if existingIP, exists := existingInterfaces[expectedLifName]; exists {
			if existingIP == datalifIp {
				m.log.Info("Data LIF already exists with correct IP", "lifName", expectedLifName, "ip", datalifIp)
				continue
			} else {
				m.log.Error(fmt.Errorf("data LIF exists but with different IP"), "skipping", "lifName", expectedLifName, "existing", existingIP, "expected", datalifIp)
				continue // Don't try to fix IP mismatches for now
			}
		}

		// Create missing data LIF
		m.log.Info("Creating missing data LIF", "lifName", expectedLifName, "ip", datalifIp)
		selectedNodeUUID := nodesUUIDs[i%len(nodesUUIDs)]
		dataLifOpts := networkInterfaceOptions{
			svmUUID:   svmUUID,
			svmName:   svmName,
			ipAddress: datalifIp,
			lifName:   expectedLifName,
			nodeUUID:  selectedNodeUUID,
			isDataLif: true,
		}
		if err := m.createNetworkInterfaceForSvm(ctx, ontapClient, dataLifOpts); err != nil {
			return fmt.Errorf("failed to create missing data LIF %s: %w", expectedLifName, err)
		}
	}

	return nil
}

// validateAndEnsureManagementLIF validates management LIF exists and creates if missing
func (m *SvmManager) validateAndEnsureManagementLIF(ctx context.Context, ontapClient *ontapv1.Ontap, svmUUID, svmName, managementIP, nodeUUID string) error {
	existingInterfaces, err := m.getExistingNetworkInterfaces(ctx, ontapClient, svmUUID)
	if err != nil {
		return err
	}

	if existingIP, exists := existingInterfaces[managementLifTag]; exists {
		if existingIP == managementIP {
			m.log.Info("Management LIF already exists with correct IP", "ip", managementIP)
			return nil
		} else {
			m.log.Error(fmt.Errorf("management LIF exists but with different IP"), "skipping", "existing", existingIP, "expected", managementIP)
			return nil // Don't try to fix IP mismatches for now
		}
	}

	// Create missing management LIF
	m.log.Info("Creating missing management LIF", "ip", managementIP)
	mgmtLifOpts := networkInterfaceOptions{
		svmUUID:   svmUUID,
		svmName:   svmName,
		ipAddress: managementIP,
		lifName:   managementLifTag,
		nodeUUID:  nodeUUID,
		isDataLif: false,
	}
	if err := m.createNetworkInterfaceForSvm(ctx, ontapClient, mgmtLifOpts); err != nil {
		return fmt.Errorf("failed to create missing management LIF: %w", err)
	}

	return nil
}

// EnsureCompleteSVM ensures a complete SVM exists with all required components, creating missing parts
func (m *SvmManager) EnsureCompleteSVM(ctx context.Context, opts CreateSVMOptions) error {
	m.log.Info("Ensuring complete SVM state", "projectId", opts.ProjectID)

	// First check if SVM exists
	existingUUID, foundClient, err := m.GetSVMByName(ctx, opts.ProjectID)
	if err != nil {
		if errors.Is(err, ErrSvmNotFound) {
			// SVM doesn't exist, create it completely
			m.log.Info("SVM not found, creating complete SVM", "projectId", opts.ProjectID)
			return m.CreateSVM(ctx, opts)
		}
		return fmt.Errorf("failed to check existing SVM: %w", err)
	}

	// SVM exists, validate and ensure all components are complete
	m.log.Info("SVM exists, validating completeness", "projectId", opts.ProjectID, "uuid", *existingUUID)
	return m.validateAndEnsureCompleteSVMState(ctx, foundClient, *existingUUID, opts.ProjectID, opts)
}

// GetSVMByName searches all clients for a running SVM matching svmName (or svmName-mc).
// Returns the UUID, the ONTAP client where the SVM is running, and an error.
func (m *SvmManager) GetSVMByName(ctx context.Context, svmName string) (*string, *ontapv1.Ontap, error) {
	var fallbackName string
	if !strings.HasSuffix(svmName, "-mc") {
		fallbackName = fmt.Sprintf("%s-mc", svmName)
	}

	for _, rc := range m.clients {
		if rc == nil || rc.SVM == nil {
			continue
		}

		params := s_vm.NewSvmCollectionGetParamsWithContext(ctx)
		svmGetOK, err := rc.SVM.SvmCollectionGet(params, nil)
		if err != nil {
			m.log.Error(err, "failed to fetch SVMs from client, trying next")
			continue
		}

		if len(svmGetOK.Payload.SvmResponseInlineRecords) == 0 {
			continue
		}

		var (
			primaryUUID  *string
			fallbackUUID *string
		)

		for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
			if svm.Name == nil || svm.UUID == nil {
				continue
			}
			switch *svm.Name {
			case svmName:
				primaryUUID = svm.UUID
			case fallbackName:
				fallbackUUID = svm.UUID
			}
		}

		// Check primary name first, then fallback (-mc)
		if uuid, ok := m.isRunningSVM(ctx, rc, primaryUUID, svmName); ok {
			return uuid, rc, nil
		}
		if uuid, ok := m.isRunningSVM(ctx, rc, fallbackUUID, fallbackName); ok {
			return uuid, rc, nil
		}
	}

	attemptedNames := []string{svmName}
	if fallbackName != "" {
		attemptedNames = append(attemptedNames, fallbackName)
	}
	m.log.Info("SVM not found after trying all known names on all clients", "requestedName", svmName, "attemptedNames", attemptedNames)
	return nil, nil, ErrSvmNotFound
}

// isRunningSVM checks whether a single SVM candidate is in "running" state.
// Returns the UUID and true if it is running, nil and false otherwise.
func (m *SvmManager) isRunningSVM(ctx context.Context, ontapClient *ontapv1.Ontap, uuid *string, name string) (*string, bool) {
	if uuid == nil {
		return nil, false
	}

	getParams := s_vm.NewSvmGetParamsWithContext(ctx)
	getParams.SetUUID(*uuid)

	svmInfo, err := ontapClient.SVM.SvmGet(getParams, nil)
	if err != nil {
		m.log.Error(err, "failed to get SVM state", "name", name, "uuid", *uuid)
		return nil, false
	}
	if svmInfo.Payload == nil || svmInfo.Payload.State == nil {
		return nil, false
	}
	if *svmInfo.Payload.State == "running" {
		m.log.Info("Found running SVM", "name", name, "uuid", *uuid)
		return uuid, true
	}

	m.log.Info("Ignoring SVM because it is not running", "name", name, "uuid", *uuid, "state", *svmInfo.Payload.State)
	return nil, false
}

// waitForSvmReady polls until the SVM exists and is in a "running" state.
func (m *SvmManager) waitForSvmReady(ctx context.Context, svmName string) (string, error) {
	m.log.Info("waiting for SVM to be ready", "svmName", svmName)

	var uuid string
	err := retry.Do(func() error {
		svmUUID, foundClient, err := m.GetSVMByName(ctx, svmName)
		if err != nil {
			if errors.Is(err, ErrSvmNotFound) {
				m.log.Info("SVM not found by name yet, retrying...", "svmName", svmName)
				return err
			}
			m.log.Error(err, "Failed to get SVM by name, retrying...", "svmName", svmName)
			return err
		}

		getParams := s_vm.NewSvmGetParamsWithContext(ctx)
		getParams.SetUUID(*svmUUID)

		svmInfo, err := foundClient.SVM.SvmGet(getParams, nil)
		if err != nil {
			m.log.Error(err, "Failed to get SVM details after finding by name, retrying...", "svmName", svmName, "uuid", svmUUID)
			return err
		}

		if svmInfo.Payload == nil || svmInfo.Payload.State == nil {
			m.log.Info("SVM found, but state information is missing, retrying...", "svmName", svmName)
			return fmt.Errorf("SVM found but state information is missing")
		}

		currentState := *svmInfo.Payload.State
		m.log.Info("Checking SVM state", "svmName", svmName, "uuid", svmUUID, "state", currentState)
		if currentState != "running" {
			m.log.Info("SVM exists but is not yet running", "state", currentState, "svmName", svmName)
			return fmt.Errorf("svm exist but not in running state yet:%s", currentState)
		}

		if svmInfo.Payload.Nvme != nil && svmInfo.Payload.Nvme.Enabled != nil && *svmInfo.Payload.Nvme.Enabled {
			m.log.Info("SVM is ready and NVMe is enabled", "svmName", svmName, "uuid", svmUUID, "state", currentState)
			uuid = *svmUUID
			return nil
		}

		m.log.Info("SVM is running but NVMe is not yet enabled, retrying...", "svmName", svmName)
		return fmt.Errorf("SVM is running but NVMe is not yet enabled")
	},
		retry.Attempts(10),
		retry.MaxDelay(5*time.Second),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		return "", fmt.Errorf("svm %q did not become ready:%w", svmName, err)
	}

	return uuid, nil
}
