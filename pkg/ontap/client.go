// Package ontap constructs ONTAP API clients from the extension controller configuration.
package ontap

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	ontapgoclient "github.com/metal-stack/ontap-go/pkg/client"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

// New constructs one ONTAP API client per configured cluster without performing I/O.
func New(config config.ControllerConfiguration) ([]*ontapv1.Ontap, error) {
	var ontapConfigs []ontapgoclient.Config

	for _, clusterConfig := range config.Clusters {
		ontapConfigs = append(ontapConfigs, ontapgoclient.Config{
			BasicAuth: &ontapgoclient.BasicAuthConfig{
				User:     clusterConfig.Username,
				Password: clusterConfig.Password,
			},
			TLS: &ontapgoclient.TLSConfig{
				InsecureTLS: new(true),
			},
			ApiURL: fmt.Sprintf("https://%s/api", clusterConfig.IPAddress),
		})
	}

	metroClusterClient, err := ontapgoclient.NewMetroClusterClient(ontapConfigs)
	if err != nil {
		return nil, err
	}
	if metroClusterClient == nil || len(*metroClusterClient) == 0 {
		return nil, fmt.Errorf("couldn't initialize any ONTAP clients")
	}

	mcClients := *metroClusterClient
	clients := make([]*ontapv1.Ontap, 0, len(mcClients))
	for i := range mcClients {
		clients = append(clients, &mcClients[i])
	}

	return clients, nil
}

// NewConnected constructs ONTAP clients and verifies connectivity to every configured cluster.
func NewConnected(ctx context.Context, log logr.Logger, config config.ControllerConfiguration) ([]*ontapv1.Ontap, error) {
	clients, err := New(config)
	if err != nil {
		return nil, err
	}

	if err := checkClients(ctx, log, config, clients); err != nil {
		return nil, err
	}

	return clients, nil
}

// CheckConnectivity verifies connectivity to every configured cluster.
func CheckConnectivity(ctx context.Context, log logr.Logger, config config.ControllerConfiguration) error {
	clients, err := New(config)
	if err != nil {
		return err
	}

	return checkClients(ctx, log, config, clients)
}

func checkClients(ctx context.Context, log logr.Logger, config config.ControllerConfiguration, clients []*ontapv1.Ontap) error {
	var errs []error
	for i, ontapClient := range clients {
		clusterName := config.Clusters[i].Name

		params := cluster.NewClusterGetParamsWithContext(ctx)
		result, err := ontapClient.Cluster.ClusterGet(params, nil)
		if err != nil {
			err := fmt.Errorf("failed to connect to ONTAP cluster %q: %w", clusterName, err)
			errs = append(errs, err)
			log.Error(err, "ONTAP cluster health check failed", "cluster", clusterName)
			continue
		}
		if result == nil || result.Payload == nil {
			err := fmt.Errorf("ONTAP cluster %q returned an empty response", clusterName)
			errs = append(errs, err)
			log.Error(err, "ONTAP cluster health check failed", "cluster", clusterName)
			continue
		}

		log.Info("Successfully connected to ONTAP cluster", "cluster", clusterName)
	}

	return errors.Join(errs...)
}
