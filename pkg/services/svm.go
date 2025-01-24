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
	fmt.Printf("Creating SVM: %s ", svmName)

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
	_, _, err := ontapClient.SVM.SvmCreate(params, nil)

	if err != nil {
		return fmt.Errorf("failed to create SVM: %w", err)
	}
	fmt.Println("SVM created successfully.")
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
		return "", nil
	}
	log.Info("before foor loop in getsvm by name")
	for _, svm := range svmGetOK.Payload.SvmResponseInlineRecords {
		if svm.Name != nil && *svm.Name == svmName {
			if svm.UUID != nil {
				return *svm.UUID, nil
			}
			return "", fmt.Errorf("UUID not available for the SVM named %s", svmName)
		}
	}
	log.Info("after foor loop in getsvm by name")
	return "", ErrNotFound
}
