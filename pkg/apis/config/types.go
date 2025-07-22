package config

import (
	"fmt"
	"net/netip"

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
	// IPAddress of the cluster
	IPAddress string
	// Username is the user to connect to the cluster management ip for cluster A
	Username string
	// Password is the admin password to access the cluster management ip for cluster A
	Password string
}

func (c *ControllerConfiguration) Validate() error {
	for _, cluster := range c.Clusters {
		if cluster.Username == "" || cluster.Password == "" {
			return fmt.Errorf("missing fields in config: cluster: %s", cluster.Name)
		}

		if _, err := netip.ParseAddr(cluster.IPAddress); err != nil {
			return fmt.Errorf("given ipaddress of cluster:%s is malformed %w", cluster.Name, err)
		}
	}

	return nil
}
