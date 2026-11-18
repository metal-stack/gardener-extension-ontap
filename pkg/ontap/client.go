package ontap

import (
	"context"
	"errors"
	"fmt"

	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	ontapgoclient "github.com/metal-stack/ontap-go/pkg/client"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

type Client struct {
	*ontapv1.Ontap
	clusterName string
}

func NewClients(config config.ControllerConfiguration) ([]*Client, error) {
	var clients = make([]*Client, 0, len(config.Clusters))

	for _, cluster := range config.Clusters {
		c, err := ontapgoclient.NewAPIClient(ontapgoclient.Config{
			BasicAuth: &ontapgoclient.BasicAuthConfig{
				User:     cluster.Username,
				Password: cluster.Password,
			},
			TLS: &ontapgoclient.TLSConfig{
				InsecureTLS: new(true),
			},
			ApiURL: fmt.Sprintf("https://%s/api", cluster.IPAddress),
		})
		if err != nil {
			return nil, fmt.Errorf("unable to create ontap client: %w", err)
		}

		clients = append(clients, &Client{
			Ontap:       c,
			clusterName: cluster.Name,
		})
	}

	return clients, nil
}

func CheckConnectivity(ctx context.Context, clients []*Client) error {
	var errs []error

	for _, client := range clients {
		result, err := client.Cluster.ClusterGet(cluster.NewClusterGetParamsWithContext(ctx), nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to connect to ontap cluster %q: %w", client.clusterName, err))
			continue
		}

		if result == nil || result.Payload == nil {
			errs = append(errs, fmt.Errorf("ontap cluster %q returned an empty response", client.clusterName))
			continue
		}
	}

	return errors.Join(errs...)
}
