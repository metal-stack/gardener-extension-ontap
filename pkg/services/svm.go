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

// CreateSVM creates an SVM for the given user/project
func CreateSVM(log logr.Logger, ontapClient *ontapv1.Ontap, svmName string) error {
	log.Info("Creating SVM", "name", svmName)

	// First check if the SVM already exists to avoid duplicates
	_, err := GetSVMByName(log, ontapClient, svmName)
	if err == nil {
		// SVM already exists
		log.Info("SVM already exists, skipping creation", "name", svmName)
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		// There was an error checking for the SVM
		return fmt.Errorf("error checking if SVM exists: %w", err)
	}

	// SVM does not exist, create it
	params := s_vm.NewSvmCreateParams()
	params.Info = &models.Svm{
		Name: &svmName,
		Iscsi: &models.SvmInlineIscsi{
			Allowed: pointer.Pointer(true),
			Enabled: pointer.Pointer(true),
		},
		Nvme: &models.SvmInlineNvme{
			Enabled: pointer.Pointer(true),
			Allowed: pointer.Pointer(true),
		},
	}

	// Debug: Log request parameters
	log.Info("Sending SVM create request", "params", fmt.Sprintf("%+v", params))

	// Add detailed error handling
	resp, _, err := ontapClient.SVM.SvmCreate(params, nil)
	if err != nil {
		// Log the detailed error
		log.Error(err, "Failed to create SVM", "name", svmName)

		// Check for specific error types
		if apiErr, ok := err.(*s_vm.SvmCreateDefault); ok {
			log.Error(err, "API Error", "code", apiErr.Code(), "message", apiErr.Error())
			if apiErr.GetPayload() != nil {
				log.Error(err, "API Error Payload", "payload", fmt.Sprintf("%+v", apiErr.GetPayload()))
			}
		}

		return fmt.Errorf("failed to create SVM: %w", err)
	}

	// The response might be nil or have a nil payload, but that's okay
	// The ONTAP API might return a job response or a direct response
	if resp != nil && resp.Payload != nil {
		log.Info("SVM creation response received",
			"name", svmName,
			"payload", fmt.Sprintf("%+v", resp.Payload))
	} else {
		log.Info("SVM creation initiated with empty response", "name", svmName)
	}

	// Check if the SVM was actually created
	uuid, err := GetSVMByName(log, ontapClient, svmName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// SVM is not found, but that's expected as creation might be async
			log.Info("SVM not found immediately after creation request, will check on next reconciliation", "name", svmName)
			return nil
		}
		return fmt.Errorf("error checking if SVM was created: %w", err)
	}

	log.Info("SVM created successfully", "name", svmName, "uuid", uuid)
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
