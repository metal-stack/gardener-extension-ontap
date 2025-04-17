package ontap

import (
	"github.com/metal-stack/gardener-extension-ontap/pkg/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TridentConfig configuration resource which configures the trident csi driver
// see: https://github.com/NetApp/trident/blob/master/helm/trident-operator/values.yaml
type TridentConfig struct {
	metav1.TypeMeta

	// SvmIpaddresses are the ip adresses provided for the svm to create and/or call the endpoint
	SvmIpaddresses common.SvmIpaddresses
}

type Protocols []Protocol
type Protocol string
