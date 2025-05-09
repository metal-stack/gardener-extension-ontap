package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	"github.com/metal-stack/ontap-go/api/client/networking"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
	"github.com/metal-stack/ontap-go/api/client/storage"
	"github.com/metal-stack/ontap-go/api/models"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/metal-stack/gardener-extension-ontap/pkg/common"
)

var (
	// ErrNotFound is returned if the svm was not found
	ErrNotFound = errors.New("NotFound")
	// ErrAlreadyExists is returned when the enitity already exists
	ErrAlreadyExists = errors.New("AlreadyExists")

	ErrSeedSecretMissing = errors.New("SeedSecretMissing")
)

// findSuitableAggregate fetches all aggregates, filters them, and selects the best one
// based on available space and usage percentage.
func findSuitableAggregate(log logr.Logger, ontapClient *ontapv1.Ontap) (*string, error) {
	log.Info("Finding suitable aggregate...")

	params := storage.NewAggregateCollectionGetParams()
	// Request specific fields needed for logic to optimize the call
	params.SetFields([]string{"uuid", "name", "state", "space"})
	// Increase limit if many aggregates are expected
	// params.SetMaxRecords(pointer.Pointer(int64(100)))

	result, err := ontapClient.Storage.AggregateCollectionGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch aggregates: %w", err)
	}

	if result.Payload == nil || result.Payload.NumRecords == nil || *result.Payload.NumRecords == 0 {
		return nil, errors.New("no aggregates found in the cluster")
	}

	type candidateAggregate struct {
		UUID           string
		Name           string
		AvailableBytes int64
		UsagePercent   float64
		TotalSizeBytes int64
	}

	var candidates []candidateAggregate

	log.Info("Filtering aggregates", "totalFound", *result.Payload.NumRecords)
	for _, record := range result.Payload.AggregateResponseInlineRecords {
		// Basic validation
		if record.UUID == nil || record.Name == nil || record.State == nil || record.Space == nil || record.Space.BlockStorage == nil {
			log.Info("Skipping aggregate due to missing essential fields", "record", record) // Log cautiously
			continue
		}

		// Filter by state (only 'online' aggregates)
		if *record.State != "online" {
			log.Info("Skipping aggregate due to state", "name", *record.Name, "state", *record.State)
			continue
		}

		// Filter aggregates with zero size (shouldn't happen but good practice)
		totalSize := record.Space.BlockStorage.Size
		if totalSize == nil || *totalSize <= 0 {
			log.Info("Skipping aggregate with zero or missing size", "name", *record.Name)
			continue
		}

		available := record.Space.BlockStorage.Available
		used := record.Space.BlockStorage.Used
		if available == nil || used == nil {
			log.Info("Skipping aggregate with missing available/used space info", "name", *record.Name)
			continue
		}

		usagePercent := float64(*used) / float64(*totalSize)

		candidates = append(candidates, candidateAggregate{
			UUID:           *record.UUID,
			Name:           *record.Name,
			AvailableBytes: *available,
			UsagePercent:   usagePercent,
			TotalSizeBytes: *totalSize,
		})
		log.Info("Aggregate is a candidate", "name", *record.Name, "available", *available, "usage", usagePercent*100)
	}

	if len(candidates) == 0 {
		return nil, errors.New("no suitable 'online' non-root aggregates found")
	}

	// Sort candidates by available space descending (most available first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].AvailableBytes > candidates[j].AvailableBytes
	})

	log.Info("Sorted aggregate candidates by available space", "count", len(candidates))

	// Select the best candidate based on usage threshold (e.g., < 90%)
	usageThreshold := 0.90
	for _, candidate := range candidates {
		log.Info("Checking candidate", "name", candidate.Name, "usage", candidate.UsagePercent*100, "threshold", usageThreshold*100)
		if candidate.UsagePercent < usageThreshold {
			log.Info("Selected aggregate", "name", candidate.Name, "uuid", candidate.UUID, "available", candidate.AvailableBytes, "usage", candidate.UsagePercent*100)
			return &candidate.UUID, nil // Return pointer to the UUID string
		}
		log.Info("Candidate usage too high", "name", candidate.Name, "usage", candidate.UsagePercent*100)
	}

	// If we reach here, all suitable aggregates were above the usage threshold
	return nil, fmt.Errorf("no suitable aggregate found below %.0f%% usage threshold", usageThreshold*100)
}

// GetBroadcastDomainUUIDByName fetches the UUID of a broadcast domain given its name.
func GetBroadcastDomainUUIDByName(log logr.Logger, ontapClient *ontapv1.Ontap, domainName string) (string, error) {
	log.Info("fetching broadcast domain UUID by name", "name", domainName)

	params := networking.NewNetworkEthernetBroadcastDomainsGetParams()
	params.Name = &domainName

	result, err := ontapClient.Networking.NetworkEthernetBroadcastDomainsGet(params, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch broadcast domains: %w", err)
	}

	if result.Payload == nil || result.Payload.NumRecords == nil || *result.Payload.NumRecords == 0 || len(result.Payload.BroadcastDomainResponseInlineRecords) == 0 {
		log.Error(fmt.Errorf("broadcast domain not found"), "Broadcast domain not found", "name", domainName)
		return "", ErrNotFound
	}

	for _, record := range result.Payload.BroadcastDomainResponseInlineRecords {
		if record.Name != nil && *record.Name == domainName {
			if record.UUID == nil {
				return "", fmt.Errorf("found broadcast domain '%s' but it has no UUID", domainName)
			}
			log.Info("Found broadcast domain", "name", domainName, "uuid", *record.UUID)
			return *record.UUID, nil
		}
	}
	log.Error(fmt.Errorf("broadcast domain not found after checking records"), "Broadcast domain not found", "name", domainName)
	return "", ErrNotFound
}

// getFirstNodeInCluster fetches the first node name found in the ONTAP cluster
func getFirstNodeInCluster(log logr.Logger, ontapClient *ontapv1.Ontap) (string, error) {
	log.Info("Fetching first available node in cluster...")

	params := cluster.NewNodesGetParams()
	params.SetFields([]string{"uuid", "name"})

	result, err := ontapClient.Cluster.NodesGet(params, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch nodes: %w", err)
	}
	for _, node := range result.Payload.NodeResponseInlineRecords {
		log.Info("Node in Response", "nodes", node)
	}

	if result.Payload == nil {
		return "", errors.New("no node information returned")
	}
	nodeUUID := *result.Payload.NodeResponseInlineRecords[0].UUID

	return nodeUUID.String(), nil
}

// createNetworkInterfaceForSvm creates a network interface for the given SVM
func createNetworkInterfaceForSvm(log logr.Logger, ontapClient *ontapv1.Ontap, svmUUID string,
	svmName string, ipAddress string, netmask string, lifName string,
	nodeUUID string, broadcastDomainUUID string, isDataLif bool) error {

	log.Info("Creating network interface", "svm", svmName, "lifName", lifName, "ip", ipAddress, "node", nodeUUID)

	params := networking.NewNetworkIPInterfacesCreateParams()
	// Create the basic interface structure
	interfaceInfo := &models.IPInterface{
		Name:    pointer.Pointer(lifName),
		Enabled: pointer.Pointer(true),
		Svm: &models.IPInterfaceInlineSvm{
			UUID: pointer.Pointer(svmUUID),
		},
	}

	interfaceInfo.IP = &models.IPInfo{
		Address: (*models.IPAddress)(pointer.Pointer(ipAddress)),
		Netmask: (*models.IPNetmask)(pointer.Pointer(netmask)),
	}

	// Add location information
	location := &models.IPInterfaceInlineLocation{}
	location.HomeNode = &models.IPInterfaceInlineLocationInlineHomeNode{
		UUID: pointer.Pointer(nodeUUID),
	}
	location.BroadcastDomain = &models.IPInterfaceInlineLocationInlineBroadcastDomain{
		UUID: pointer.Pointer(broadcastDomainUUID),
	}

	interfaceInfo.Location = location
	if isDataLif {
		// NVMe/TCP policy
		interfaceInfo.ServicePolicy = &models.IPInterfaceInlineServicePolicy{
			Name: pointer.Pointer("default-data-nvme-tcp"),
		}
	}
	if !isDataLif {
		// Management policy
		interfaceInfo.ServicePolicy = &models.IPInterfaceInlineServicePolicy{
			Name: pointer.Pointer("default-management"),
		}
	}
	// interfaceInfo.Vip = pointer.Pointer(true)
	params.SetInfo(interfaceInfo)
	_, err := ontapClient.Networking.NetworkIPInterfacesCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create network interface %s for SVM %s: %w", lifName, svmName, err)
	}
	log.Info("Successfully created network interface", "lifName", lifName, "svm", svmName, "ip", ipAddress)
	return nil
}

// CreateSVM creates an SVM and sets up network interfaces on a selected node
func CreateSVM(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string, seedClient client.Client,
	SvmIpaddresses common.SvmIpaddresses) error {
	log.Info("Creating SVM with IPs", "name", projectId, "managementLif", SvmIpaddresses.ManagementLif, "dataLif", SvmIpaddresses.DataLif)

	// 1. Find a suitable aggregate for the SVM
	chosenAggregateUUID, err := findSuitableAggregate(log, ontapClient)
	if err != nil {
		return fmt.Errorf("failed to find a suitable aggregate for SVM %s: %w", projectId, err)
	}
	log.Info("Assigning SVM to selected aggregate", "svm", projectId, "aggregateUUID", *chosenAggregateUUID)

	// 2. Get broadcast domain UUID for network interfaces
	defaultBroadcastDomainName := "Default"
	broadcastDomainUUID, err := GetBroadcastDomainUUIDByName(log, ontapClient, defaultBroadcastDomainName)
	if err != nil {
		log.Info("Broadcast domain lookup failed, continuing with interface creation", "domain", defaultBroadcastDomainName, "error", err)
		// Non-fatal, continue with empty broadcast domain UUID
		broadcastDomainUUID = ""
	}

	// 3. Create the SVM without network interfaces
	params := s_vm.NewSvmCreateParams()
	params.Info = &models.Svm{
		Name: &projectId,
		SvmInlineAggregates: []*models.SvmInlineAggregatesInlineArrayItem{
			{UUID: chosenAggregateUUID},
		},
		Nvme: &models.SvmInlineNvme{
			Enabled: pointer.Pointer(true),
			Allowed: pointer.Pointer(true),
		},
	}

	log.Info("Sending SVM create request", "params", fmt.Sprintf("%+v", params))
	_, _, err = ontapClient.SVM.SvmCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create SVM %s: %w", projectId, err)
	}
	log.Info("SVM created successfully", "name", projectId, "aggregateUUID", *chosenAggregateUUID)

	// 4. Wait for SVM to be ready and get its UUID
	svmUUID, err := waitForSvmReady(log, ontapClient, projectId)
	if err != nil {
		return fmt.Errorf("SVM '%s' was not ready: %w", projectId, err)
	}
	log.Info("SVM is ready", "projectId", projectId, "uuid", svmUUID)

	// 5. Get a node for network interface placement
	nodeUUID, err := getFirstNodeInCluster(log, ontapClient)
	if err != nil {
		log.Info("Failed to get a node for LIF placement, will create LIFs without node affinity", "error", err)
		return err
	}

	// 6. Create data LIF
	err = createNetworkInterfaceForSvm(log, ontapClient, svmUUID, projectId,
		SvmIpaddresses.DataLif, "24", common.DataLifTag,
		nodeUUID, broadcastDomainUUID, true)
	if err != nil {
		return fmt.Errorf("failed to create data LIF for SVM %s: %w", projectId, err)
	}

	// 7. Create management LIF
	err = createNetworkInterfaceForSvm(log, ontapClient, svmUUID, projectId,
		SvmIpaddresses.ManagementLif, "24", common.ManagementLifTag,
		nodeUUID, broadcastDomainUUID, false)
	if err != nil {
		return fmt.Errorf("failed to create management LIF for SVM %s: %w", projectId, err)
	}

	// 8. Create user and secret
	log.Info("Proceeding to create user and secret for SVM", "svm", projectId)
	if err = CreateUserAndSecret(ctx, log, ontapClient, projectId, shootNamespace, seedClient, svmUUID); err != nil {
		return fmt.Errorf("SVM %s created, but failed to create user and secret: %w", projectId, err)
	}

	log.Info("Successfully completed SVM creation and setup", "svm", projectId)
	return nil
}

func GetAllSVM(log logr.Logger, ontapClient *ontapv1.Ontap) error {
	log.Info("Fetching all SVMs...")

	if ontapClient == nil || ontapClient.SVM == nil {
		return fmt.Errorf("API client or SVM service is not initialized")
	}

	params := s_vm.NewSvmCollectionGetParams()
	svmGetOK, err := ontapClient.SVM.SvmCollectionGet(params, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch SVMs: %w", err)
	}

	if svmGetOK == nil || svmGetOK.Payload == nil {
		log.Info("No SVM data available.")
		return nil
	}

	if svmGetOK.Payload.NumRecords != nil {
		fmt.Printf("Number of SVM records: %d\n", *svmGetOK.Payload.NumRecords)
	} else {
		log.Info("Number of SVM records is not available.")
	}

	for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
		if svm.UUID != nil && svm.Name != nil {
			fmt.Printf("SVM UUID: %s, Name: %s\n", *svm.UUID, *svm.Name)
		} else {
			log.Info("One of the required SVM details (UUID or Name) is not available.")
		}
	}

	return nil
}

// Returns a svm by inputting the svmName, i.e. projectId
func GetSVMByName(log logr.Logger, ontapClient *ontapv1.Ontap, svmName string) (string, error) {

	if ontapClient == nil || ontapClient.SVM == nil {
		return "", fmt.Errorf("API client or SVM service is not initialized")
	}

	params := s_vm.NewSvmCollectionGetParams()
	svmGetOK, err := ontapClient.SVM.SvmCollectionGet(params, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch SVMs: %w", err)
	}

	log.Info("Checking for SVM with name", "name", svmName)

	if len(svmGetOK.Payload.SvmResponseInlineRecords) == 0 {
		log.Info("No SVMs found in the response")
		return "", ErrNotFound
	}

	for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
		if svm.Name != nil && *svm.Name == svmName {
			if svm.UUID != nil {
				log.Info("Found SVM", "name", svmName, "uuid", *svm.UUID)
				return *svm.UUID, nil
			}
			return "", ErrNotFound
		}
	}

	log.Info("SVM not found", "name", svmName)
	return "", ErrNotFound
}

// waitForSvmReady polls until the SVM exists and is in a "running" state.
func waitForSvmReady(log logr.Logger, ontapClient *ontapv1.Ontap, svmName string) (string, error) {
	maxRetries := 10
	retryDelay := 6 * time.Second

	log.Info("waiting for SVM to be ready", "svmName", svmName)

	for i := 0; i < maxRetries; i++ {
		svmUUID, err := GetSVMByName(log, ontapClient, svmName)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				log.Info("SVM not found by name yet, retrying...", "svmName", svmName, "attempt", i+1)
				if i < maxRetries-1 {
					time.Sleep(retryDelay)
				}
				continue
			}
			log.Error(err, "Failed to get SVM by name, retrying...", "svmName", svmName, "attempt", i+1)
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue
		}

		getParams := s_vm.NewSvmGetParams()
		getParams.SetUUID(svmUUID)

		svmInfo, err := ontapClient.SVM.SvmGet(getParams, nil)
		if err != nil {
			log.Error(err, "Failed to get SVM details after finding by name, retrying...", "svmName", svmName, "uuid", svmUUID, "attempt", i+1)
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue
		}

		if svmInfo.Payload == nil || svmInfo.Payload.State == nil {
			log.Info("SVM found, but state information is missing, retrying...", "svmName", svmName, "attempt", i+1)
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue
		}

		currentState := *svmInfo.Payload.State
		log.Info("Checking SVM state", "svmName", svmName, "uuid", svmUUID, "state", currentState, "attempt", i+1)
		if currentState == "running" {
			log.Info("SVM is ready", "svmName", svmName, "uuid", svmUUID, "state", currentState)
			return svmUUID, nil
		}

		log.Info("SVM exists but is not yet running", "state", currentState, "svmName", svmName, "attempt", i+1)
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// If loop finishes, SVM was not found or did not become ready
	return "", fmt.Errorf("SVM '%s' did not become ready after %d attempts", svmName, maxRetries)
}
