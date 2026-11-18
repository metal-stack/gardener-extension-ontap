package ontap

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/storage"
	"github.com/metal-stack/ontap-go/api/models"
	mockstorage "github.com/metal-stack/ontap-go/test/mocks/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestWriteClient(t *testing.T) {
	t.Run("selects client with fewest volumes", func(t *testing.T) {
		service1, service2 := &mockstorage.ClientService{}, &mockstorage.ClientService{}
		service1.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a1"), UUID: new("u1"), VolumeCount: new(int64(30))},
				},
			}}, nil)
		service2.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a2"), UUID: new("u2"), VolumeCount: new(int64(5))},
				},
			}}, nil)

		clients := Clients([]*Client{
			newStorageClient(service1, "cluster-a"),
			newStorageClient(service2, "cluster-b"),
		})

		got, err := clients.WriteClient(t.Context())
		require.NoError(t, err)
		assert.Same(t, clients[1], got)
	})

	t.Run("error on api failure", func(t *testing.T) {
		service := &mockstorage.ClientService{}
		service.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("connection refused"))

		clients := Clients([]*Client{
			newStorageClient(service, "cluster-a"),
		})

		_, err := clients.WriteClient(t.Context())
		require.Error(t, err)
	})

	t.Run("error on empty response", func(t *testing.T) {
		service := &mockstorage.ClientService{}
		service.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: nil}, nil)

		clients := Clients([]*Client{
			newStorageClient(service, "cluster-a"),
		})

		_, err := clients.WriteClient(t.Context())
		require.ErrorContains(t, err, "empty aggregate response")
	})

	t.Run("no clients", func(t *testing.T) {
		clients := Clients([]*Client{})

		_, err := clients.WriteClient(t.Context())
		require.Error(t, err)
	})
}

func TestWriteClientFailoverBehavior(t *testing.T) {
	t.Run("first client down fails entire selection", func(t *testing.T) {
		service1, service2 := &mockstorage.ClientService{}, &mockstorage.ClientService{}
		service1.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("cluster unreachable"))
		service2.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a1"), UUID: new("u1"), VolumeCount: new(int64(5))},
				},
			}}, nil)

		clients := Clients([]*Client{
			newStorageClient(service1, "cluster-a"),
			newStorageClient(service2, "cluster-b"),
		})

		_, err := clients.WriteClient(t.Context())
		require.Error(t, err, "WriteClient fails hard if any client is unreachable")
		assert.Contains(t, err.Error(), "cluster unreachable")
	})

	t.Run("nil volume counts skipped, still selects correctly", func(t *testing.T) {
		service1, service2 := &mockstorage.ClientService{}, &mockstorage.ClientService{}
		service1.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a1"), UUID: new("u1"), VolumeCount: nil},
					{Name: new("a2"), UUID: new("u2"), VolumeCount: nil},
				},
			}}, nil)
		service2.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a3"), UUID: new("u3"), VolumeCount: new(int64(10))},
				},
			}}, nil)

		clients := Clients([]*Client{
			newStorageClient(service1, "cluster-a"),
			newStorageClient(service2, "cluster-b"),
		})

		got, err := clients.WriteClient(t.Context())
		require.NoError(t, err)
		assert.Same(t, clients[0], got, "nil volumes counted as 0, so client1 appears emptier")
	})
}

func newStorageClient(service *mockstorage.ClientService, clusterName string) *Client {
	return &Client{
		Ontap:       &ontapv1.Ontap{Storage: service},
		clusterName: clusterName,
		log:         logr.Discard(),
	}
}
