package v1alpha1

import (
	healthcheckconfigv1alpha1 "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ShootOntapResourceName = "extension-ontap-shoot"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ControllerConfiguration configuration resource
type ControllerConfiguration struct {
	metav1.TypeMeta `json:",inline"`

	Clusters []Cluster `json:"clusters,omitempty"`

	// HealthCheckConfig is the config for the health check controller
	// +optional
	HealthCheckConfig *healthcheckconfigv1alpha1.HealthCheckConfig `json:"healthCheckConfig,omitempty"`
}

type Cluster struct {
	// Name of the cluster
	Name string `json:"name,omitempty"`
	// IPAddress of the cluster
	IPAddress string `json:"ipaddress,omitempty"`
	// Username is the user to connect to the cluster management ip for cluster A
	Username string `json:"username,omitempty"`
	// Password is the admin password to access the cluster management ip for cluster A
	Password string `json:"password,omitempty"`
}
