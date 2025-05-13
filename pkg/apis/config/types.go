package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	healthcheckconfig "github.com/gardener/gardener/extensions/pkg/apis/config"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ControllerConfiguration defines the configuration for the ontap controller.
type ControllerConfiguration struct {
	metav1.TypeMeta

	// AdminAuthSecretRef references to the secret which contains the auth credentials to connect to the cluster management ip
	AdminAuthSecretRef string

	// AuthSecretNamespace references the seed namespace where the secret is stored to access the cluster management ip
	AuthSecretNamespace string

	// HealthCheckConfig is the config for the health check controller
	HealthCheckConfig *healthcheckconfig.HealthCheckConfig
}
