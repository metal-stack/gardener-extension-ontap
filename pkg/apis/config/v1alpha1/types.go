package v1alpha1

import (
	healthcheckconfigv1alpha1 "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ControllerConfiguration configuration resource
type ControllerConfiguration struct {
	metav1.TypeMeta `json:",inline"`

	// OntapEndpoint is the endpoint where the ontap rest api is accessible, can be a hostname or a ip address.
	OntapEndpoint string `json:"svmName,omitempty"`

	// AuthSecretRef references to the secret which contains the auth credentials to connect to the ontap rest api.
	AuthSecretRef string `json:"authSecretRef,omitempty"`

	// HealthCheckConfig is the config for the health check controller
	// +optional
	HealthCheckConfig *healthcheckconfigv1alpha1.HealthCheckConfig `json:"healthCheckConfig,omitempty"`
}
