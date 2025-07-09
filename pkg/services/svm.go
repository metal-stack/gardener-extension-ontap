package services

import (
	"context"
	"errors"
	"fmt"
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
	// ErrNotFound is returned if the svm was not found
	ErrNotFound = errors.New("NotFound")
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

type SvnManager struct {
	log         logr.Logger
	ontapClient *ontapv1.Ontap
	seedClient  client.Client
}

func NewSvnManager(log logr.Logger, ontapClient *ontapv1.Ontap, seedClient client.Client) *SvnManager {
	return &SvnManager{
		log:         log,
		ontapClient: ontapClient,
		seedClient:  seedClient,
	}
}

// CreateSVM creates an SVM and sets up network interfaces on a selected node
func (m *SvnManager) CreateSVM(ctx context.Context, opts CreateSVMOptions) error {
	m.log.Info("Creating SVM with IPs", "name", opts.ProjectID, "managementLif", opts.SvmIpaddresses.ManagementLif, "dataLifs", opts.SvmIpaddresses.DataLifs)

	// 1. Get a node for network interface placement and aggregate selection
	nodeUUID, err := m.getFirstNodeInCluster()
	if err != nil {
		return fmt.Errorf("failed to get a node for SVM creation: %w", err)
	}

	// 2. Find a suitable aggregate on the selected node
	chosenAggregateUUID, err := m.findSuitableAggregateForNode(nodeUUID)
	if err != nil {
		return fmt.Errorf("failed to find a suitable aggregate for SVM %s: %w", opts.ProjectID, err)
	}
	m.log.Info("Assigning SVM to selected aggregate", "svm", opts.ProjectID, "aggregateUUID", *chosenAggregateUUID, "nodeUUID", nodeUUID)

	// 3. Create the SVM without network interfaces
	params := &s_vm.SvmCreateParams{
		Info: &models.Svm{
			Name: &opts.ProjectID,
			SvmInlineAggregates: []*models.SvmInlineAggregatesInlineArrayItem{
				{UUID: chosenAggregateUUID},
			},
			Nvme: &models.SvmInlineNvme{
				Enabled: pointer.Pointer(true),
				Allowed: pointer.Pointer(true),
			},
		},
	}

	m.log.Info("Sending SVM create request", "params", fmt.Sprintf("%+v", params))
	_, _, err = m.ontapClient.SVM.SvmCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create SVM %s: %w", opts.ProjectID, err)
	}
	m.log.Info("SVM created successfully", "name", opts.ProjectID, "aggregateUUID", *chosenAggregateUUID)

	// 4. Wait for SVM to be ready and get its UUID
	svmUUID, err := m.waitForSvmReady(opts.ProjectID)
	if err != nil {
		return fmt.Errorf("SVM '%s' was not ready: %w", opts.ProjectID, err)
	}
	m.log.Info("SVM is ready", "projectId", opts.ProjectID, "uuid", svmUUID)

	// 5. Create data LIFs
	for _, datalifIp := range opts.SvmIpaddresses.DataLifs {
		dataLifOpts := networkInterfaceOptions{
			svmUUID:   svmUUID,
			svmName:   opts.ProjectID,
			ipAddress: datalifIp,
			lifName:   dataLifTag,
			// needs to be adjusted so ips are created distributed on both nodes, PR is open for this already
			nodeUUID:  nodeUUID,
			isDataLif: true,
		}
		err = m.createNetworkInterfaceForSvm(dataLifOpts)
		if err != nil {
			return fmt.Errorf("failed to create data LIF for SVM %s: %w", opts.ProjectID, err)
		}
	}

	// 6. Create management LIF
	mgmtLifOpts := networkInterfaceOptions{
		svmUUID:   svmUUID,
		svmName:   opts.ProjectID,
		ipAddress: opts.SvmIpaddresses.ManagementLif,
		lifName:   managementLifTag,
		nodeUUID:  nodeUUID,
		isDataLif: false,
	}
	err = m.createNetworkInterfaceForSvm(mgmtLifOpts)
	if err != nil {
		return fmt.Errorf("failed to create management LIF for SVM %s: %w", opts.ProjectID, err)
	}

	// 7. Create user and secret in svmSeedSecretNamespace namespace
	m.log.Info("Proceeding to create user and secret for SVM", "svm", opts.ProjectID)
	userOpts := userAndSecretOptions{
		projectID:              opts.ProjectID,
		svmSeedSecretNamespace: opts.SvmSeedSecretNamespace,
		seedClient:             m.seedClient,
		svmUUID:                svmUUID,
	}
	if err = m.CreateUserAndSecret(ctx, userOpts); err != nil {
		return fmt.Errorf("SVM %s created, but failed to create user and secret: %w", opts.ProjectID, err)
	}

	m.log.Info("Successfully completed SVM creation and setup", "svm", opts.ProjectID)
	return nil
}

// findSuitableAggregateForNode fetches all aggregates on a specific node and selects the one
// with the most available space that isn't a root aggregate
func (m *SvnManager) findSuitableAggregateForNode(nodeUUID string) (*string, error) {
	m.log.Info("Finding suitable aggregate on node", "nodeUUID", nodeUUID)

	params := storage.NewAggregateCollectionGetParams()
	params.SetFields([]string{"uuid", "name", "state", "space", "node"})

	result, err := m.ontapClient.Storage.AggregateCollectionGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch aggregates: %w", err)
	}

	if result.Payload == nil || result.Payload.NumRecords == nil || *result.Payload.NumRecords == 0 {
		return nil, errors.New("no aggregates found in the cluster")
	}

	var (
		bestUUID     *string
		maxAvailable int64 = -1
		bestName     string
	)

	m.log.Info("Filtering aggregates on node", "nodeUUID", nodeUUID, "totalFound", *result.Payload.NumRecords)
	for _, record := range result.Payload.AggregateResponseInlineRecords {
		// Skip if missing essential fields
		if record.UUID == nil || record.Name == nil || record.State == nil ||
			record.Space == nil || record.Space.BlockStorage == nil || record.Node == nil {
			continue
		}

		// Skip if not on our selected node
		if record.Node.UUID == nil || *record.Node.UUID != nodeUUID {
			continue
		}

		// Skip if not online
		if *record.State != "online" {
			m.log.Info("Skipping offline aggregate", "name", *record.Name)
			continue
		}
		// We might wanna skip root aggregate too

		// Skip if no available space info
		available := record.Space.BlockStorage.Available
		if available == nil || *available <= 0 {
			continue
		}

		// Keep track of the aggregate with the most available space
		if *available > maxAvailable {
			maxAvailable = *available
			bestUUID = record.UUID
			bestName = *record.Name
			m.log.Info("Found better aggregate", "name", *record.Name, "available", *available)
		}
	}

	if bestUUID == nil {
		return nil, fmt.Errorf("no suitable aggregates found on node %s", nodeUUID)
	}

	m.log.Info("Selected aggregate with most available space", "name", bestName, "uuid", *bestUUID, "availableBytes", maxAvailable)
	return bestUUID, nil
}

// getFirstNodeInCluster fetches the first node name found in the ONTAP cluster
// Needs to be changed, waiting for Netapp answer
func (m *SvnManager) getFirstNodeInCluster() (string, error) {
	m.log.Info("Fetching first available node in cluster...")

	params := cluster.NewNodesGetParams()
	params.SetFields([]string{"uuid", "name"})

	result, err := m.ontapClient.Cluster.NodesGet(params, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch nodes: %w", err)
	}
	for _, node := range result.Payload.NodeResponseInlineRecords {
		m.log.Info("Node in Response", "nodes", node)
	}

	if result.Payload == nil {
		return "", errors.New("no node information returned")
	}
	nodeUUID := *result.Payload.NodeResponseInlineRecords[0].UUID

	return nodeUUID.String(), nil
}

// createNetworkInterfaceForSvm creates a network interface for the given SVM
func (m *SvnManager) createNetworkInterfaceForSvm(opts networkInterfaceOptions) error {

	m.log.Info("Creating network interface", "svm", opts.svmName, "lifName", opts.lifName, "ip", opts.ipAddress, "node", opts.nodeUUID)

	// setting default netmask to 24, bc 32 is only possible if bgp peer is available and vip lif can be created
	netmask := "24"
	params := networking.NewNetworkIPInterfacesCreateParams()
	// Create the basic interface structure
	interfaceInfo := &models.IPInterface{
		Name:    pointer.Pointer(opts.lifName),
		Enabled: pointer.Pointer(true),
		Svm: &models.IPInterfaceInlineSvm{
			UUID: pointer.Pointer(opts.svmUUID),
		},
	}

	paramsBgp := networking.NewNetworkIPBgpPeerGroupsGetParams()
	bgpres, err := m.ontapClient.Networking.NetworkIPBgpPeerGroupsGet(paramsBgp, nil)
	if err != nil {
		return err
	}
	m.log.Info("bgp response", "bgp", bgpres)
	// A bgp neighbor is there
	if bgpres.Payload.NumRecords != nil && *bgpres.Payload.NumRecords != 0 {
		netmask = "32"
		interfaceInfo.Vip = pointer.Pointer(true)
	}
	interfaceInfo.IP = &models.IPInfo{
		Address: (*models.IPAddress)(pointer.Pointer(opts.ipAddress)),
		Netmask: (*models.IPNetmask)(pointer.Pointer(netmask)),
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
	_, err = m.ontapClient.Networking.NetworkIPInterfacesCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create network interface %s for SVM %s: %w", opts.lifName, opts.svmName, err)
	}

	m.log.Info("Successfully created network interface", "lifName", opts.lifName, "svm", opts.svmName, "ip", opts.ipAddress)
	return nil
}

// Returns a svm by inputting the svmName, i.e. projectId
func (m *SvnManager) GetSVMByName(svmName string) (string, error) {

	if m.ontapClient == nil || m.ontapClient.SVM == nil {
		return "", fmt.Errorf("API client or SVM service is not initialized")
	}

	params := s_vm.NewSvmCollectionGetParams()
	svmGetOK, err := m.ontapClient.SVM.SvmCollectionGet(params, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch SVMs: %w", err)
	}

	m.log.Info("Checking for SVM with name", "name", svmName)

	if len(svmGetOK.Payload.SvmResponseInlineRecords) == 0 {
		m.log.Info("No SVMs found in the response")
		return "", ErrNotFound
	}

	for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
		if svm.Name != nil && *svm.Name == svmName {
			if svm.UUID != nil {
				m.log.Info("Found SVM", "name", svmName, "uuid", *svm.UUID)
				return *svm.UUID, nil
			}
			return "", ErrNotFound
		}
	}

	m.log.Info("SVM not found", "name", svmName)
	return "", ErrNotFound
}

// waitForSvmReady polls until the SVM exists and is in a "running" state.
func (m *SvnManager) waitForSvmReady(svmName string) (string, error) {
	m.log.Info("waiting for SVM to be ready", "svmName", svmName)

	var uuid string
	err := retry.Do(func() error {
		svmUUID, err := m.GetSVMByName(svmName)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				m.log.Info("SVM not found by name yet, retrying...", "svmName", svmName)
				return err
			}
			m.log.Error(err, "Failed to get SVM by name, retrying...", "svmName", svmName)
			return err
		}

		getParams := s_vm.NewSvmGetParams()
		getParams.SetUUID(svmUUID)

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

		m.log.Info("SVM is ready", "svmName", svmName, "uuid", svmUUID, "state", currentState)
		uuid = svmUUID
		return nil
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
