package v1alpha1

import (
	healthcheckconfigv1alpha1 "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ControllerConfiguration configuration resource
type ControllerConfiguration struct {
	metav1.TypeMeta `json:",inline"`

	// AuthSecretRef references to the secret which contains the auth credentials to connect to the ontap rest api for cluster A
	AdminAuthSecretRef_ClusterA string `json:"adminAuthSecretclusterA,omitempty"`

	// AuthSecretNamespace references the shoot namespace where the secret is stored to access the svm for cluster A
	AuthSecretNamespace_ClusterA string `json:"authSecretNamespaceclusterA,omitempty"`

	// AuthSecretRef references to the secret which contains the auth credentials to connect to the ontap rest api for cluster B
	AdminAuthSecretRef_ClusterB string `json:"adminAuthSecretclusterB,omitempty"`

	// AuthSecretNamespace references the shoot namespace where the secret is stored to access the svm for cluster B
	AuthSecretNamespace_ClusterB string `json:"authSecretNamespaceclusterB,omitempty"`

	// HealthCheckConfig is the config for the health check controller
	// +optional
	HealthCheckConfig *healthcheckconfigv1alpha1.HealthCheckConfig `json:"healthCheckConfig,omitempty"`
}
