package v1alpha1

import (
	"errors"
	"slices"

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

	// SVMName is the name of the storage virtual machine, can be a hostname or a ip address.
	SVMName string `json:"svmName,omitempty"`

	// Protocols to use to mount the volume, only NVMe is used for now.
	Protocols Protocols `json:"protocols,omitempty"`

	// AuthSecretRef references to the secret which contains the auth credentials to connect to the svm
	AuthSecretRef string `json:"authSecretRef,omitempty"`
}
type Protocols []Protocol
type Protocol string

func (config *TridentConfig) IsValid(log logr.Logger) bool {
	if slices.Contains(config.Protocols, "nvme") {
		log.Error(errors.New("protocol nvme is required"), "err", "protocols", config.Protocols)
		return false
	}

	// FIXME more validations

	return true
}

func (config *TridentConfig) ConfigureDefaults() error {
	// FIXME consider defaulting
	return nil
}
