package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateNoClusters(t *testing.T) {
	err := (&ControllerConfiguration{}).Validate()

	require.ErrorContains(t, err, "clusters is empty")
}

func TestValidateValid(t *testing.T) {
	controllerConfig := &ControllerConfiguration{Clusters: []Cluster{
		{Name: "cluster-a", IPAddress: "192.168.10.11", Username: "admin", Password: "password"},
	}}

	require.NoError(t, controllerConfig.Validate())
}
