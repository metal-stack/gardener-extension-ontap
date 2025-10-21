package trident

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/go-logr/logr"
	"github.com/metal-stack/metal-lib/pkg/pointer"
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
	log         logr.Logger
	ontapClient *ontapv1.Ontap
	seedClient  client.Client
}

func NewSvmManager(log logr.Logger, ontapClient *ontapv1.Ontap, seedClient client.Client) *SvmManager {
	return &SvmManager{
		log:         log,
		ontapClient: ontapClient,
		seedClient:  seedClient,
	}
}

// CreateSVM creates an SVM and sets up network interfaces on a selected node
func (m *SvmManager) CreateSVM(ctx context.Context, opts CreateSVMOptions) error {
	m.log.Info("Creating SVM with IPs", "name", opts.ProjectID, "managementLif", opts.SvmIpaddresses.ManagementLif, "dataLifs", opts.SvmIpaddresses.DataLifs)

	// 1. Get a node for network interface placement and aggregate selection
	nodesUUIDs, err := m.getAllNodesInCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get a node for SVM creation: %w", err)
	}

	// 2. Trident needs a svm assigned, but is not exclusive to that aggregate
	// Ontap uses all aggregates per default to assign aggregates.
	agrgp := storage.NewAggregateCollectionGetParamsWithContext(ctx)
	// Got these from the Swagger UI, i don't think there is another way
	agrcget, err := m.ontapClient.Storage.AggregateCollectionGet(agrgp, nil)
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
				Enabled: pointer.Pointer(true),
				Allowed: pointer.Pointer(true),
			},
		},
		Context: ctx,
	}

	m.log.Info("Sending SVM create request", "params", fmt.Sprintf("%+v", params))
	if _, _, err = m.ontapClient.SVM.SvmCreate(params, nil); err != nil {
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
		if err := m.createNetworkInterfaceForSvm(ctx, dataLifOpts); err != nil {
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
	if err := m.createNetworkInterfaceForSvm(ctx, mgmtLifOpts); err != nil {
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
	if err := m.CreateUserAndSecret(ctx, userOpts); err != nil {
		return fmt.Errorf("SVM %s created, but failed to create user and secret: %w", opts.ProjectID, err)
	}

	m.log.Info("Successfully completed SVM creation and setup", "svm", opts.ProjectID)
	return nil
}

// getAllNodesInCluster fetches the first node name found in the ONTAP cluster
// Needs to be changed, waiting for Netapp answer
func (m *SvmManager) getAllNodesInCluster(ctx context.Context) ([]string, error) {
	m.log.Info("Fetching first available node in cluster...")

	var nodeUUIDs []string
	params := cluster.NewNodesGetParamsWithContext(ctx)
	params.SetFields([]string{"uuid", "name"})

	result, err := m.ontapClient.Cluster.NodesGet(params, nil)
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
func (m *SvmManager) createNetworkInterfaceForSvm(ctx context.Context, opts networkInterfaceOptions) error {

	m.log.Info("Creating network interface", "svm", opts.svmName, "lifName", opts.lifName, "ip", opts.ipAddress, "node", opts.nodeUUID)

	params := networking.NewNetworkIPInterfacesCreateParamsWithContext(ctx)
	// Create the basic interface structure
	interfaceInfo := &models.IPInterface{
		Name:    pointer.Pointer(opts.lifName),
		Enabled: pointer.Pointer(true),
		Svm: &models.IPInterfaceInlineSvm{
			UUID: pointer.Pointer(opts.svmUUID),
		},
	}

	paramsBgp := networking.NewNetworkIPBgpPeerGroupsGetParamsWithContext(ctx)
	bgpres, err := m.ontapClient.Networking.NetworkIPBgpPeerGroupsGet(paramsBgp, nil)
	if err != nil {
		return err
	}
	m.log.Info("bgp response", "bgp", bgpres)
	// A bgp neighbor is there
	// setting default netmask to 24, bc 32 is only possible if bgp peer is available and vip lif can be created
	netmask := "24"
	if bgpres.Payload.NumRecords != nil && *bgpres.Payload.NumRecords != 0 {
		netmask = "32"
		interfaceInfo.Vip = pointer.Pointer(true)
	}

	var (
		address = pointer.Pointer(models.IPAddress(opts.ipAddress))
		mask    = pointer.Pointer(models.IPNetmask(netmask))
	)
	interfaceInfo.IP = &models.IPInfo{
		Address: address,
		Netmask: mask,
	}

	// Add location information
	location := &models.IPInterfaceInlineLocation{}
	location.HomeNode = &models.IPInterfaceInlineLocationInlineHomeNode{
		UUID: pointer.Pointer(opts.nodeUUID),
	}
	interfaceInfo.Location = location
	if opts.isDataLif {
		// NVMe/TCP policy
		interfaceInfo.ServicePolicy = &models.IPInterfaceInlineServicePolicy{
			Name: pointer.Pointer("default-data-nvme-tcp"),
		}
	}
	if !opts.isDataLif {
		// Management policy
		interfaceInfo.ServicePolicy = &models.IPInterfaceInlineServicePolicy{
			Name: pointer.Pointer("default-management"),
		}
	}
	params.SetInfo(interfaceInfo)
	if _, err := m.ontapClient.Networking.NetworkIPInterfacesCreate(params, nil); err != nil {
		return fmt.Errorf("failed to create network interface %s for SVM %s: %w", opts.lifName, opts.svmName, err)
	}

	m.log.Info("Successfully created network interface", "lifName", opts.lifName, "svm", opts.svmName, "ip", opts.ipAddress)
	return nil
}

// validateAndEnsureCompleteSVMState validates all components of an SVM and creates missing parts
func (m *SvmManager) validateAndEnsureCompleteSVMState(ctx context.Context, svmUUID, svmName string, opts CreateSVMOptions) error {
	m.log.Info("Validating complete SVM state", "svmName", svmName, "uuid", svmUUID)

	// 1. Validate SVM is running and NVMe enabled
	if err := m.validateSVMRunningState(ctx, svmUUID, svmName); err != nil {
		return err
	}

	// 2. Get cluster nodes for LIF creation (if needed)
	nodesUUIDs, err := m.getAllNodesInCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes for SVM validation: %w", err)
	}

	// 3. Validate and ensure data LIFs exist
	if err := m.validateAndEnsureDataLIFs(ctx, svmUUID, svmName, opts.SvmIpaddresses.DataLifs, nodesUUIDs); err != nil {
		return err
	}

	// 4. Validate and ensure management LIF exists
	if err := m.validateAndEnsureManagementLIF(ctx, svmUUID, svmName, opts.SvmIpaddresses.ManagementLif, nodesUUIDs[0]); err != nil {
		return err
	}

	userOpts := userAndSecretOptions{
		projectID:              svmName,
		shootNamespace:         opts.ShootNamespace,
		svmSeedSecretNamespace: opts.SvmSeedSecretNamespace,
		seedClient:             m.seedClient,
		svmUUID:                svmUUID,
	}
	if err := m.CreateUserAndSecret(ctx, userOpts); err != nil {
		return fmt.Errorf("failed to ensure user and secret for SVM %s: %w", svmName, err)
	}

	m.log.Info("SVM state validation and completion successful", "svmName", svmName)
	return nil
}

// validateSVMRunningState checks if SVM is in running state with NVMe enabled
func (m *SvmManager) validateSVMRunningState(ctx context.Context, svmUUID, svmName string) error {
	getParams := s_vm.NewSvmGetParamsWithContext(ctx)
	getParams.SetUUID(svmUUID)

	svmInfo, err := m.ontapClient.SVM.SvmGet(getParams, nil)
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
func (m *SvmManager) getExistingNetworkInterfaces(ctx context.Context, svmUUID string) (map[string]string, error) {
	params := networking.NewNetworkIPInterfacesGetParamsWithContext(ctx)
	params.SetSvmUUID(&svmUUID)
	fields := []string{"name", "ip.address"}
	params.SetFields(fields)

	result, err := m.ontapClient.Networking.NetworkIPInterfacesGet(params, nil)
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
func (m *SvmManager) validateAndEnsureDataLIFs(ctx context.Context, svmUUID, svmName string, expectedDataLifs []string, nodesUUIDs []string) error {
	existingInterfaces, err := m.getExistingNetworkInterfaces(ctx, svmUUID)
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
		if err := m.createNetworkInterfaceForSvm(ctx, dataLifOpts); err != nil {
			return fmt.Errorf("failed to create missing data LIF %s: %w", expectedLifName, err)
		}
	}

	return nil
}

// validateAndEnsureManagementLIF validates management LIF exists and creates if missing
func (m *SvmManager) validateAndEnsureManagementLIF(ctx context.Context, svmUUID, svmName, managementIP, nodeUUID string) error {
	existingInterfaces, err := m.getExistingNetworkInterfaces(ctx, svmUUID)
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
	if err := m.createNetworkInterfaceForSvm(ctx, mgmtLifOpts); err != nil {
		return fmt.Errorf("failed to create missing management LIF: %w", err)
	}

	return nil
}

// EnsureCompleteSVM ensures a complete SVM exists with all required components, creating missing parts
func (m *SvmManager) EnsureCompleteSVM(ctx context.Context, opts CreateSVMOptions) error {
	m.log.Info("Ensuring complete SVM state", "projectId", opts.ProjectID)

	// First check if SVM exists
	existingUUID, err := m.GetSVMByName(ctx, opts.ProjectID)
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
	return m.validateAndEnsureCompleteSVMState(ctx, *existingUUID, opts.ProjectID, opts)
}

// Returns a svm by inputting the svmName, i.e. projectId
func (m *SvmManager) GetSVMByName(ctx context.Context, svmName string) (*string, error) {
	if m.ontapClient == nil || m.ontapClient.SVM == nil {
		return nil, fmt.Errorf("API client or SVM service is not initialized")
	}

	params := s_vm.NewSvmCollectionGetParamsWithContext(ctx)
	svmGetOK, err := m.ontapClient.SVM.SvmCollectionGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SVMs: %w", err)
	}

	if len(svmGetOK.Payload.SvmResponseInlineRecords) == 0 {
		m.log.Info("No SVMs found in the response")
		return nil, ErrSvmNotFound
	}

	namesToCheck := []string{svmName}
	if !strings.HasSuffix(svmName, "-mc") {
		namesToCheck = append(namesToCheck, fmt.Sprintf("%s-mc", svmName))
	}

	for idx, nameToFind := range namesToCheck {
		if idx == 0 {
			m.log.Info("Checking for SVM with name", "name", nameToFind)
		} else {
			m.log.Info("Primary SVM name not found, trying fallback", "fallbackName", nameToFind)
		}

		for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
			if svm.Name != nil && *svm.Name == nameToFind {
				if svm.UUID != nil {
					m.log.Info("Found SVM", "name", nameToFind, "uuid", *svm.UUID)
					return svm.UUID, nil
				}
			}
		}
	}

	m.log.Info("SVM not found after trying all known names", "requestedName", svmName, "attemptedNames", namesToCheck)
	return nil, ErrSvmNotFound
}

// waitForSvmReady polls until the SVM exists and is in a "running" state.
func (m *SvmManager) waitForSvmReady(ctx context.Context, svmName string) (string, error) {
	m.log.Info("waiting for SVM to be ready", "svmName", svmName)

	var uuid string
	err := retry.Do(func() error {
		svmUUID, err := m.GetSVMByName(ctx, svmName)
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

		svmInfo, err := m.ontapClient.SVM.SvmGet(getParams, nil)
		if err != nil {
			m.log.Error(err, "Failed to get SVM details after finding by name, retrying...", "svmName", svmName, "uuid", svmUUID)
			return err
		}

		if svmInfo.Payload == nil || svmInfo.Payload.State == nil {
			m.log.Info("SVM found, but state information is missing, retrying...", "svmName", svmName)
			return err
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
