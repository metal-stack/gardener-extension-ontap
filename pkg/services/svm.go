package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
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

// CreateSVM creates an SVM for the given user and or project and returns the network interfaces
func CreateSVM(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string, seedClient client.Client, SvmIpaddresses common.SvmIpaddresses) error {
	log.Info("Creating SVM with ips", "name", projectId, "managementLif", SvmIpaddresses.ManagementLif, "dataLif", SvmIpaddresses.DataLif)
	// SVM does not exist, create it
	params := s_vm.NewSvmCreateParams()
	params.Info = &models.Svm{
		Name: &projectId,
		Iscsi: &models.SvmInlineIscsi{
			Allowed: pointer.Pointer(true),
			Enabled: pointer.Pointer(true),
		},

		//Commented out for now till nvme license comes
		// SvmInlineIPInterfaces: []*models.IPInterfaceSvm{
		// 	{
		// 		Name: pointer.Pointer(common.DataLifTag),
		// 		IP: &models.IPInterfaceSvmInlineIP{
		// 			Address: (*models.IPAddressReadcreate)(pointer.Pointer(addresses.DataLif)),
		// 			Netmask: (*models.IPNetmaskCreateonly)(pointer.Pointer("255.255.255.0")),
		// 		},
		// 	},
		// 	{
		// 		Name: pointer.Pointer(common.ManagementLifTag),
		// 		IP: &models.IPInterfaceSvmInlineIP{
		// 			Address: (*models.IPAddressReadcreate)(pointer.Pointer(addresses.ManagementLif)),
		// 			Netmask: (*models.IPNetmaskCreateonly)(pointer.Pointer("255.255.255.0")),
		// 		},
		// 	},
		// },
		// Need license for Simulator
		// Add NVMe protocol when supported by your ONTAP version
		// Nvme: &models.SvmInlineNvme{
		//     Enabled: pointer.Pointer(true),
		//     Allowed: pointer.Pointer(true),
		// },
	}

	log.Info("Sending SVM create request!", "params", fmt.Sprintf("%+v", params))
	//Not doing anyhting with the response
	_, _, err := ontapClient.SVM.SvmCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create SVM: %w", err)
	}
	log.Info("SVM created successfully", "name", projectId, "network Interfaces", SvmIpaddresses)
	log.Info("Creating user and secret with network information",
		"projectId", projectId,
		"dataLif", SvmIpaddresses.DataLif,
		"managementLif", SvmIpaddresses.ManagementLif)
	if err = CreateUserAndSecret(ctx, log, ontapClient, projectId, shootNamespace, seedClient); err != nil {
		return fmt.Errorf("failed to create user and secret: %w", err)
	}
	log.Info("created user and secret for svm and secret for shoot and seed", "projectId", projectId)
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
