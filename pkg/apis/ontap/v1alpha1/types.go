package v1alpha1

import (
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// FIXME rename const
	ShootCsiDriverLvmResourceName = "extension-ontap"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TridentConfig configuration resource which configures the trident csi driver
type TridentConfig struct {
	metav1.TypeMeta `json:",inline"`

	//SvmIpAddresses are the endpoints needed by the
	SvmIpaddresses SvmIpaddresses `json:"svmIpaddresses,omitempty"`
}

// SvmIpaddresses contains the network interface addresses for a Storage Virtual Machine (SVM)
type SvmIpaddresses struct {
	// DataLif are the IP addresses for data operations
	DataLifs []string `json:"dataLif,omitempty"`

	// ManagementLif is the IP address for management operations
	ManagementLif string `json:"managementLif,omitempty"`
}

func (config *TridentConfig) IsValid(log logr.Logger) bool {
	// if slices.Contains(config.Protocols, "nvme") {
	// 	log.Error(errors.New("protocol nvme is required"), "err", "protocols", config.Protocols)
	// 	return false
	// }

	// FIXME more validations

	return true
}

func (config *TridentConfig) ConfigureDefaults(svmName *string, svmSecretRef *string) error {

	// if config.Protocols == nil || len(config.Protocols) == 0 {
	// 	if defaultProtocols != nil {
	// 		config.Protocols = *defaultProtocols
	// 	}
	// }
	return nil
}
