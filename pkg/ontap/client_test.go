package ontap

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	"github.com/metal-stack/ontap-go/api/models"
	mockcluster "github.com/metal-stack/ontap-go/test/mocks/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

func TestNewClients(t *testing.T) {
	clients, err := NewClients(logr.Discard(), config.ControllerConfiguration{Clusters: []config.Cluster{
		{Name: "cluster-a", IPAddress: "192.168.10.11", Username: "admin", Password: "password-a"},
		{Name: "cluster-b", IPAddress: "192.168.10.12", Username: "admin", Password: "password-b"},
	}})

	require.NoError(t, err)
	require.Len(t, clients, 2)
}

func TestCheckClients(t *testing.T) {
	tests := []struct {
		name           string
		healthy        []bool
		emptyResponses []bool
		wantErr        string
	}{
		{
			name:    "all healthy",
			healthy: []bool{true, true},
		},
		{
			name:    "one unhealthy",
			healthy: []bool{true, false},
			wantErr: `cluster "cluster-b"`,
		},
		{
			name:           "empty response",
			healthy:        []bool{true},
			emptyResponses: []bool{true},
			wantErr:        `cluster "cluster-a" returned an empty response`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controllerConfig := config.ControllerConfiguration{}
			var clients Clients

			for i, healthy := range tt.healthy {
				name := fmt.Sprintf("cluster-%c", 'a'+i)
				controllerConfig.Clusters = append(controllerConfig.Clusters, config.Cluster{Name: name})

				service := &mockcluster.ClientService{}
				switch {
				case i < len(tt.emptyResponses) && tt.emptyResponses[i]:
					service.On("ClusterGet", mock.Anything, mock.Anything).
						Return(&cluster.ClusterGetOK{Payload: nil}, nil)
				case healthy:
					service.On("ClusterGet", mock.Anything, mock.Anything).
						Return(&cluster.ClusterGetOK{Payload: &models.Cluster{}}, nil)
				default:
					service.On("ClusterGet", mock.Anything, mock.Anything).
						Return(nil, fmt.Errorf("connection refused"))
				}

				clients = append(clients, &Client{Ontap: &ontapv1.Ontap{Cluster: service}, clusterName: name})
			}

			err := clients.CheckConnectivity(t.Context())
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
