package services

import (
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
	"github.com/metal-stack/ontap-go/api/models"
)

var (
	// ErrNotFound is returned if the svm was not found
	ErrNotFound = errors.New("NotFound")
	// ErrAlreadyExists is returned when the enitity already exists
	ErrAlreadyExists = errors.New("AlreadyExists")
)

// CreateSVM creates an SVM for the given user/project and returns the network interfaces
func CreateSVM(log logr.Logger, ontapClient *ontapv1.Ontap, svmName string) (string, string, error) {
	log.Info("Creating SVM", "name", svmName)

	// First check if the SVM already exists to avoid duplicates
	uuid, err := GetSVMByName(log, ontapClient, svmName)
	if err == nil {
		// SVM already exists,get network interfaces
		//not prod ready
		dataLif, managementLif, err := GetSVMNetworkInterfaces(log, ontapClient, uuid)
		if err != nil {
			log.Error(err, "Failed to get network interfaces for existing SVM", "name", svmName, "uuid", uuid)
			return "", "", fmt.Errorf("failed to get network interfaces for existing SVM: %w", err)
		}
		log.Info("Using existing SVM interfaces", "dataLif", dataLif, "managementLif", managementLif)
		return dataLif, managementLif, nil
	} else if !errors.Is(err, ErrNotFound) {
		// There was an error checking for the SVM
		return "", "", fmt.Errorf("error checking if SVM exists: %w", err)
	}

	// SVM does not exist, create it
	params := s_vm.NewSvmCreateParams()
	params.Info = &models.Svm{
		Name: &svmName,
		Iscsi: &models.SvmInlineIscsi{
			Allowed: pointer.Pointer(true),
			Enabled: pointer.Pointer(true),
		},
		// Need license for Simulator
		// Add NVMe protocol when supported by your ONTAP version
		// Nvme: &models.SvmInlineNvme{
		//     Enabled: pointer.Pointer(true),
		//     Allowed: pointer.Pointer(true),
		// },
	}

	log.Info("Sending SVM create request", "params", fmt.Sprintf("%+v", params))
	resp, _, err := ontapClient.SVM.SvmCreate(params, nil)
	if err != nil {
		// Log the detailed error
		log.Error(err, "Failed to create SVM", "name", svmName)

		// Check for specific error types
		var apiErr *s_vm.SvmCreateDefault
		if errors.As(err, &apiErr) {
			log.Error(err, "API Error", "code", apiErr.Code(), "message", apiErr.Error())
			if apiErr.GetPayload() != nil {
				log.Error(err, "API Error Payload", "payload", fmt.Sprintf("%+v", apiErr.GetPayload()))
			}
		}
		return "", "", fmt.Errorf("failed to create SVM: %w", err)
	}

	// The response might be nil or have a nil payload, but that's okay
	if resp != nil && resp.Payload != nil {
		log.Info("SVM creation response received",
			"name", svmName,
			"payload", fmt.Sprintf("%+v", resp.Payload))
	} else {
		log.Info("SVM creation initiated with empty response", "name", svmName)
	}

	// Check if the SVM was actually created
	uuid, err = GetSVMByName(log, ontapClient, svmName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			log.Info("SVM not found immediately after creation request, will check on next reconciliation", "name", svmName)
			return "", "", fmt.Errorf("SVM creation initiated but not yet available, retry later")
		}
		return "", "", fmt.Errorf("error checking if SVM was created: %w", err)
	}
	log.Info("SVM created successfully", "name", svmName, "uuid", uuid)

	//not prod ready
	dataLif, managementLif, err := SetupSVMNetworkInterfaces(log, ontapClient, uuid, svmName)
	if err != nil {
		log.Error(err, "Failed to set up network interfaces for SVM", "name", svmName, "uuid", uuid)

		// In a production environment, this is a critical error - return it
		return "", "", fmt.Errorf("SVM created but failed to configure network interfaces: %w", err)
	}

	log.Info("SVM network interfaces configured", "dataLif", dataLif, "managementLif", managementLif)
	return dataLif, managementLif, nil
}

// not ready
func FindAvailableIPs(log logr.Logger, ontapClient *ontapv1.Ontap) (string, string, error) {
	log.Info("Looking for available IPs for SVM LIFs")
	dataLIF := "192.168.1.100"
	managementLIF := "192.168.1.101"

	return dataLIF, managementLIF, nil
}

// not ready
func SetupSVMNetworkInterfaces(log logr.Logger, ontapClient *ontapv1.Ontap, svmUUID string, svmName string) (string, string, error) {
	log.Info("Setting up network interfaces for SVM", "name", svmName, "uuid", svmUUID)

	// Find available IPs for the SVM
	dataLIF, managementLIF, err := FindAvailableIPs(log, ontapClient)
	if err != nil {
		return "", "", fmt.Errorf("failed to find available IPs: %w", err)
	}

	log.Info("Using network interfaces for SVM",
		"dataLif", dataLIF,
		"managementLif", managementLIF)

	return dataLIF, managementLIF, nil
}

// not ready
func GetSVMNetworkInterfaces(log logr.Logger, ontapClient *ontapv1.Ontap, svmUUID string) (string, string, error) {
	log.Info("Getting network interfaces for SVM", "uuid", svmUUID)
	dataLIF := "192.168.1.100"
	managementLIF := "192.168.1.101"

	return dataLIF, managementLIF, nil
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

	if svmGetOK == nil || svmGetOK.Payload == nil {
		log.Info("No SVM data available.")
		return "", ErrNotFound
	}

	log.Info("Checking for SVM with name", "name", svmName)

	if svmGetOK.Payload.SvmResponseInlineRecords == nil || len(svmGetOK.Payload.SvmResponseInlineRecords) == 0 {
		log.Info("No SVMs found in the response")
		return "", ErrNotFound
	}

	for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
		if svm.Name != nil && *svm.Name == svmName {
			if svm.UUID != nil {
				log.Info("Found SVM", "name", svmName, "uuid", *svm.UUID)
				return *svm.UUID, nil
			}
			return "", fmt.Errorf("UUID not available for the SVM named %s", svmName)
		}
	}

	log.Info("SVM not found", "name", svmName)
	return "", ErrNotFound
}
