package ontap

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TridentConfig configuration resource which configures the trident csi driver
// see: https://github.com/NetApp/trident/blob/master/helm/trident-operator/values.yaml
type TridentConfig struct {
	metav1.TypeMeta

	// SvmIpaddresses are the ip addresses provided for the svm to create and/or call the endpoint
	SvmIpaddresses SvmIpaddresses
}

// SvmIpaddresses contains the network interface addresses for a Storage Virtual Machine (SVM)
type SvmIpaddresses struct {
	// DataLif is the IP address for data operations
	DataLif string

	// ManagementLif is the IP address for management operations
	ManagementLif string
}
