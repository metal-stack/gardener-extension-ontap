package ontap

import (
	"context"
	"fmt"
	"math"

	"github.com/metal-stack/ontap-go/api/client/storage"
)

// WriteClient selects the client with the fewest total volumes.
func (cs Clients) WriteClient(ctx context.Context) (*Client, error) {
	var (
		bestClient *Client
		minVolumes int64 = math.MaxInt64
	)

	for _, c := range cs {
		result, err := c.Storage.AggregateCollectionGet(storage.NewAggregateCollectionGetParamsWithContext(ctx).WithFields([]string{"volume-count"}), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get aggregate volume counts for cluster %q: %w", c.clusterName, err)
		}

		if result == nil || result.Payload == nil {
			return nil, fmt.Errorf("cluster %q returned an empty aggregate response", c.clusterName)
		}

		var (
			volumeCount int64
			nilCount    int
		)

		for _, aggr := range result.Payload.AggregateResponseInlineRecords {
			if aggr.VolumeCount == nil {
				nilCount++
				c.log.Info("aggregate with nil volume_count", "aggregate", aggr.Name, "uuid", aggr.UUID)
				continue
			}

			c.log.Info("aggregate volume_count", "aggregate", aggr.Name, "volume_count", *aggr.VolumeCount)

			volumeCount += *aggr.VolumeCount
		}

		c.log.Info("client total volume count", "volume_count", volumeCount, "aggregates", len(result.Payload.AggregateResponseInlineRecords), "nil_volume_counts", nilCount)

		if volumeCount < minVolumes {
			minVolumes = volumeCount
			bestClient = c
		}
	}

	if bestClient == nil {
		return nil, fmt.Errorf("no suitable write client found")
	}

	bestClient.log.Info("selected write client", "cluster", bestClient.clusterName, "volume_count", minVolumes)

	return bestClient, nil
}
