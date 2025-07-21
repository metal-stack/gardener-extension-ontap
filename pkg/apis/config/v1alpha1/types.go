package v1alpha1

import (
	healthcheckconfigv1alpha1 "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// AdminAuthSecretRef references to the secret which contains the auth credentials to connect to the cluster management ip for cluster A
	AuthSecretRef string `json:"authSecretRef,omitempty"`
	// AuthSecretNamespace references the seed namespace where the secret is stored to access the cluster management ip for cluster A
	AuthSecretNamespace string `json:"authSecretNamespace,omitempty"`
}
