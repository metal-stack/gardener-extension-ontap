package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

func TestValidateControllerConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		config     *config.ControllerConfiguration
		wantFields []string
	}{
		{
			name: "valid",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{Name: "cluster-a", IPAddress: "192.168.10.11", Username: "admin", Password: "password"},
			}},
		},
		{
			name:       "nil config",
			config:     nil,
			wantFields: []string{"[]"},
		},
		{
			name:       "no clusters",
			config:     &config.ControllerConfiguration{},
			wantFields: []string{"clusters"},
		},
		{
			name: "empty name",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{IPAddress: "192.168.10.11", Username: "admin", Password: "password"},
			}},
			wantFields: []string{"clusters[0].name"},
		},
		{
			name: "empty username",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{Name: "cluster-a", IPAddress: "192.168.10.11", Password: "password"},
			}},
			wantFields: []string{"clusters[0].username"},
		},
		{
			name: "empty password",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{Name: "cluster-a", IPAddress: "192.168.10.11", Username: "admin"},
			}},
			wantFields: []string{"clusters[0].password"},
		},
		{
			name: "empty ip address",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{Name: "cluster-a", Username: "admin", Password: "password"},
			}},
			wantFields: []string{"clusters[0].ipaddress"},
		},
		{
			name: "invalid ip address",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{Name: "cluster-a", IPAddress: "not-an-ip", Username: "admin", Password: "password"},
			}},
			wantFields: []string{"clusters[0].ipaddress"},
		},
		{
			name: "duplicate cluster names",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{Name: "cluster-a", IPAddress: "192.168.10.11", Username: "admin", Password: "password"},
				{Name: "cluster-a", IPAddress: "192.168.10.12", Username: "admin", Password: "password"},
			}},
			wantFields: []string{"clusters[1].name"},
		},
		{
			name: "multiple errors",
			config: &config.ControllerConfiguration{Clusters: []config.Cluster{
				{IPAddress: "not-an-ip"},
			}},
			wantFields: []string{"clusters[0].name", "clusters[0].ipaddress", "clusters[0].username", "clusters[0].password"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateControllerConfiguration(tt.config)
			require.Len(t, errs, len(tt.wantFields))
			for i, wantField := range tt.wantFields {
				assert.Equal(t, wantField, errs[i].Field)
			}
		})
	}
}
