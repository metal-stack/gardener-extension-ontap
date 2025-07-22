package config

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	healthcheckconfig "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ControllerConfiguration defines the configuration for the ontap controller.
type ControllerConfiguration struct {
	metav1.TypeMeta

	// Clusters specifies the list of clusters in this metrocluster configuration
	Clusters []Cluster

	// HealthCheckConfig is the config for the health check controller
	HealthCheckConfig *healthcheckconfig.HealthCheckConfig
}

type Cluster struct {
	// Name of the cluster
	Name string
	// AdminAuthSecretRef references to the secret which contains the auth credentials to connect to the cluster management ip for cluster A
	AuthSecretRef string
	// AuthSecretNamespace references the seed namespace where the secret is stored to access the cluster management ip for cluster A
	AuthSecretNamespace string
}

func (c *ControllerConfiguration) Validate() error {
	for _, cluster := range c.Clusters {
		if cluster.AuthSecretRef == "" || cluster.AuthSecretNamespace == "" {
			return fmt.Errorf("missing fields in config: cluster=%s, cluster secret namespace=%s, cluster secret ref=%s", cluster, cluster.AuthSecretNamespace, cluster.AuthSecretRef)
		}
	}

	return nil
}
