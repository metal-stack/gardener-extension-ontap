package trident

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
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func NewSvmManager(log logr.Logger, ontapClient *ontapv1.Ontap, seedClient client.Client) *SvnManager {
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
	m.log.Info("Proceeding to create user and secret for SVM", "svm", opts.ProjectID)
	userOpts := userAndSecretOptions{
		projectID:              opts.ProjectID,
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
func (m *SvnManager) getAllNodesInCluster(ctx context.Context) ([]string, error) {
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
func (m *SvnManager) createNetworkInterfaceForSvm(ctx context.Context, opts networkInterfaceOptions) error {

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

// Returns a svm by inputting the svmName, i.e. projectId
func (m *SvnManager) GetSVMByName(ctx context.Context, svmName string) (string, error) {

	var svmUUID *string

	if m.ontapClient == nil || m.ontapClient.SVM == nil {
		return "", fmt.Errorf("API client or SVM service is not initialized")
	}

	params := s_vm.NewSvmCollectionGetParamsWithContext(ctx)
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
				svmUUID = svm.UUID
			}
			return "", ErrNotFound
		}
	}

	if svmUUID != nil {
		// check for the seed secret, if its not there create it here, because the svm already exists but seed secret is missing
		// this can only happen on the first shoot of the project
		// if this happens on the second shoot or n shoot, something is really broken
		secretName := fmt.Sprintf("ontap-svm-%s-credentials", svmName)
		err = m.seedClient.Get(ctx, client.ObjectKeyFromObject(&corev1.Secret{ObjectMeta: v1.ObjectMeta{Name: secretName, Namespace: "kube-system"}}), nil)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				m.log.Info("seed secret does not exist even tho svm exists, changing password of svm and creating seed secret")
				m.CreateMissingSeedSecret(ctx, svmName, m.ontapClient)
			}
			return "", err
		}
	}

	m.log.Info("SVM not found", "name", svmName)
	return "", ErrNotFound
}

// waitForSvmReady polls until the SVM exists and is in a "running" state.
func (m *SvnManager) waitForSvmReady(ctx context.Context, svmName string) (string, error) {
	m.log.Info("waiting for SVM to be ready", "svmName", svmName)

	var uuid string
	err := retry.Do(func() error {
		svmUUID, err := m.GetSVMByName(ctx, svmName)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				m.log.Info("SVM not found by name yet, retrying...", "svmName", svmName)
				return err
			}
			m.log.Error(err, "Failed to get SVM by name, retrying...", "svmName", svmName)
			return err
		}

		getParams := s_vm.NewSvmGetParamsWithContext(ctx)
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

		if svmInfo.Payload.Nvme != nil && svmInfo.Payload.Nvme.Enabled != nil && *svmInfo.Payload.Nvme.Enabled {
			m.log.Info("SVM is ready and NVMe is enabled", "svmName", svmName, "uuid", svmUUID, "state", currentState)
			uuid = svmUUID
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
