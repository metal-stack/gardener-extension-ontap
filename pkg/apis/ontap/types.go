package ontap

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TridentConfig configuration resource which configures the trident csi driver
// see: https://github.com/NetApp/trident/blob/master/helm/trident-operator/values.yaml
type TridentConfig struct {
	metav1.TypeMeta

	// SVMName is the name of the storage virtual machine, can be a hostname or a ip address.
	SVMName string

	// Protocols to use to mount the volume, only NVMe is used for now.
	Protocols Protocols

	// SvmSecretRef references to the secret which contains the auth credentials and the lif ips to connect to the svm
	SVMSecretRef string

	// DataLif references to the secret which contains the auth credentials and the lif ips to connect to the svm
	DataLif string

	// ManagementLif references to the secret which contains the auth credentials and the lif ips to connect to the svm
	ManagementLif string
}

type Protocols []Protocol
type Protocol string

// # Auto generated ANF backend related fields consumed by the configurator controller.
// anfConfigurator:
//   enabled: false
//   virtualNetwork: ""
//   subnet: ""
//   subscriptionID: ""
//   tenantID: ""
//   location: ""
//   clientCredentials: ""
//   capacityPools: []
//   netappAccounts: []
//   resourceGroups: []
//   customerEncryptionKeys: {}

// # Auto generated ONTAP backend related fields consumed by the configurator controller.
// ontapConfigurator:
//   enabled: false
//   svms:
//     - fsxnID: ''
//       svmName: ''
//       protocols: []
//       authType: ''
