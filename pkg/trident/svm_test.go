package trident

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-openapi/strfmt"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	"github.com/metal-stack/ontap-go/api/client/networking"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
	"github.com/metal-stack/ontap-go/api/client/storage"
	"github.com/metal-stack/ontap-go/api/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetWriteClient(t *testing.T) {
	ctx := context.Background()

	t.Run("selects client with fewest volumes", func(t *testing.T) {
		mc1, mc2 := newMockOntapClient(), newMockOntapClient()
		mc1.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a1"), UUID: new("u1"), VolumeCount: new(int64(30))},
				},
			}}, nil)
		mc2.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a2"), UUID: new("u2"), VolumeCount: new(int64(5))},
				},
			}}, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc1.client, mc2.client}, nil)
		got, err := m.getWriteClient(ctx)
		require.NoError(t, err)
		assert.Equal(t, mc2.client, got)
	})

	t.Run("error on api failure", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("connection refused"))

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, nil)
		_, err := m.getWriteClient(ctx)
		require.Error(t, err)
	})

	t.Run("no clients", func(t *testing.T) {
		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{}, nil)
		_, err := m.getWriteClient(ctx)
		require.Error(t, err)
	})
}

func TestGetAllNodesInCluster(t *testing.T) {
	ctx := context.Background()

	t.Run("returns uuids for 2+ nodes", func(t *testing.T) {
		mc := newMockOntapClient()
		u1, u2 := strfmt.UUID("node-1"), strfmt.UUID("node-2")
		mc.cluster.On("NodesGet", mock.Anything, mock.Anything).
			Return(&cluster.NodesGetOK{Payload: &models.NodeResponse{
				NodeResponseInlineRecords: []*models.NodeResponseInlineRecordsInlineArrayItem{
					{UUID: &u1, Name: new("n1")},
					{UUID: &u2, Name: new("n2")},
				},
			}}, nil)

		m := NewSvmManager(logr.Discard(), nil, nil)
		uuids, err := m.getAllNodesInCluster(ctx, mc.client)
		require.NoError(t, err)
		assert.Equal(t, []string{"node-1", "node-2"}, uuids)
	})

	t.Run("rejects single node", func(t *testing.T) {
		mc := newMockOntapClient()
		u1 := strfmt.UUID("node-1")
		mc.cluster.On("NodesGet", mock.Anything, mock.Anything).
			Return(&cluster.NodesGetOK{Payload: &models.NodeResponse{
				NodeResponseInlineRecords: []*models.NodeResponseInlineRecordsInlineArrayItem{
					{UUID: &u1, Name: new("n1")},
				},
			}}, nil)

		m := NewSvmManager(logr.Discard(), nil, nil)
		_, err := m.getAllNodesInCluster(ctx, mc.client)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "less than 2 nodes")
	})
}

func TestGetSVMByName(t *testing.T) {
	ctx := context.Background()

	runningSvmGet := &s_vm.SvmGetOK{Payload: &models.Svm{State: new("running")}}

	t.Run("finds by primary name", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1"), UUID: new("uuid-1")},
				},
			}}, nil)
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).Return(runningSvmGet, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, nil)
		uuid, c, err := m.GetSVMByName(ctx, "proj-1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-1", *uuid)
		assert.Equal(t, mc.client, c)
	})

	t.Run("falls back to -mc suffix", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1-mc"), UUID: new("uuid-mc")},
				},
			}}, nil)
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).Return(runningSvmGet, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, nil)
		uuid, _, err := m.GetSVMByName(ctx, "proj-1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-mc", *uuid)
	})

	t.Run("not found", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{},
			}}, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, nil)
		_, _, err := m.GetSVMByName(ctx, "proj-1")
		require.ErrorIs(t, err, ErrSvmNotFound)
	})

	t.Run("skips failing client", func(t *testing.T) {
		mc1, mc2 := newMockOntapClient(), newMockOntapClient()
		mc1.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("down"))
		mc2.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1"), UUID: new("uuid-2")},
				},
			}}, nil)
		mc2.svm.On("SvmGet", mock.Anything, mock.Anything).Return(runningSvmGet, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc1.client, mc2.client}, nil)
		uuid, _, err := m.GetSVMByName(ctx, "proj-1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-2", *uuid)
	})
}

func TestGetSVMByName_MultiClientFailover(t *testing.T) {
	ctx := context.Background()
	runningSvmGet := &s_vm.SvmGetOK{Payload: &models.Svm{State: new("running")}}
	stoppedSvmGet := &s_vm.SvmGetOK{Payload: &models.Svm{State: new("stopped")}}

	t.Run("stopped on client1, found running on client2", func(t *testing.T) {
		mc1, mc2 := newMockOntapClient(), newMockOntapClient()

		// Client1 has SVM but stopped (e.g. metrocluster failover)
		mc1.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1"), UUID: new("uuid-c1")},
				},
			}}, nil)
		mc1.svm.On("SvmGet", mock.Anything, mock.Anything).Return(stoppedSvmGet, nil)

		// Client2 has SVM running (took over)
		mc2.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1"), UUID: new("uuid-c2")},
				},
			}}, nil)
		mc2.svm.On("SvmGet", mock.Anything, mock.Anything).Return(runningSvmGet, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc1.client, mc2.client}, nil)
		uuid, c, err := m.GetSVMByName(ctx, "proj-1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-c2", *uuid)
		assert.Equal(t, mc2.client, c, "should select the client where SVM is running")
	})

	t.Run("client1 down, finds -mc name on client2", func(t *testing.T) {
		mc1, mc2 := newMockOntapClient(), newMockOntapClient()

		mc1.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("connection refused"))

		// MetroCluster partner has SVM with -mc suffix
		mc2.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1-mc"), UUID: new("uuid-mc")},
				},
			}}, nil)
		mc2.svm.On("SvmGet", mock.Anything, mock.Anything).Return(runningSvmGet, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc1.client, mc2.client}, nil)
		uuid, c, err := m.GetSVMByName(ctx, "proj-1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-mc", *uuid)
		assert.Equal(t, mc2.client, c)
	})

	t.Run("prefers primary name over -mc on same client", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmCollectionGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmCollectionGetOK{Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: new("proj-1-mc"), UUID: new("uuid-mc")},
					{Name: new("proj-1"), UUID: new("uuid-primary")},
				},
			}}, nil)
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).Return(runningSvmGet, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, nil)
		uuid, _, err := m.GetSVMByName(ctx, "proj-1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-primary", *uuid, "primary name should be preferred over -mc")
	})
}

func TestIsRunningSVM(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	t.Run("nil uuid returns false", func(t *testing.T) {
		mc := newMockOntapClient()
		uuid, ok := m.isRunningSVM(ctx, mc.client, nil, "test")
		assert.Nil(t, uuid)
		assert.False(t, ok)
	})

	t.Run("api error returns false", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("connection lost"))
		id := "some-uuid"
		_, ok := m.isRunningSVM(ctx, mc.client, &id, "test")
		assert.False(t, ok)
	})

	t.Run("nil payload returns false", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).
			Return(&s_vm.SvmGetOK{Payload: nil}, nil)
		id := "some-uuid"
		_, ok := m.isRunningSVM(ctx, mc.client, &id, "test")
		assert.False(t, ok)
	})
}

func TestGetWriteClient_FailoverBehavior(t *testing.T) {
	ctx := context.Background()

	t.Run("first client down fails entire selection", func(t *testing.T) {
		// Documents current behavior: getWriteClient does NOT skip failing clients
		mc1, mc2 := newMockOntapClient(), newMockOntapClient()
		mc1.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("cluster unreachable"))
		mc2.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a1"), UUID: new("u1"), VolumeCount: new(int64(5))},
				},
			}}, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc1.client, mc2.client}, nil)
		_, err := m.getWriteClient(ctx)
		require.Error(t, err, "getWriteClient fails hard if any client is unreachable")
		assert.Contains(t, err.Error(), "cluster unreachable")
	})

	t.Run("nil volume counts skipped, still selects correctly", func(t *testing.T) {
		mc1, mc2 := newMockOntapClient(), newMockOntapClient()
		// Client1: all aggregates have nil volume counts → treated as 0
		mc1.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a1"), UUID: new("u1"), VolumeCount: nil},
					{Name: new("a2"), UUID: new("u2"), VolumeCount: nil},
				},
			}}, nil)
		// Client2: has real volume counts
		mc2.storage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
			Return(&storage.AggregateCollectionGetOK{Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: new("a3"), UUID: new("u3"), VolumeCount: new(int64(10))},
				},
			}}, nil)

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc1.client, mc2.client}, nil)
		got, err := m.getWriteClient(ctx)
		require.NoError(t, err)
		assert.Equal(t, mc1.client, got, "nil volumes counted as 0 → client1 appears emptier")
	})
}

func TestGetExistingNetworkInterfaces(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	t.Run("skips interfaces with nil name or ip", func(t *testing.T) {
		mc := newMockOntapClient()
		ip := models.IPAddress("10.0.0.1")
		mc.networking.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesGetOK{Payload: &models.IPInterfaceResponse{
				IPInterfaceResponseInlineRecords: []*models.IPInterface{
					{Name: nil, IP: &models.IPInfo{Address: &ip}},              // nil name
					{Name: new("datalif+0"), IP: nil},                          // nil IP
					{Name: new("datalif+1"), IP: &models.IPInfo{Address: &ip}}, // valid
				},
			}}, nil)

		ifaces, err := m.getExistingNetworkInterfaces(ctx, mc.client, "uuid")
		require.NoError(t, err)
		assert.Len(t, ifaces, 1)
		assert.Equal(t, "10.0.0.1", ifaces["datalif+1"])
	})
}

func TestValidateAndEnsureDataLIFs_IPMismatch(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	t.Run("tolerates ip mismatch without creating new lif", func(t *testing.T) {
		mc := newMockOntapClient()
		existingIP := models.IPAddress("10.0.0.99")
		mc.networking.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesGetOK{Payload: &models.IPInterfaceResponse{
				IPInterfaceResponseInlineRecords: []*models.IPInterface{
					{Name: new("datalif+0"), IP: &models.IPInfo{Address: &existingIP}},
				},
			}}, nil)

		// Expected IP differs from existing
		err := m.validateAndEnsureDataLIFs(ctx, mc.client, "uuid", "svm", []string{"10.0.0.1"}, []string{"n1", "n2"})
		require.NoError(t, err)
		mc.networking.AssertNotCalled(t, "NetworkIPInterfacesCreate", mock.Anything, mock.Anything)
	})
}

func TestValidateSVMRunningState(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	t.Run("ok when running with nvme", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).Return(&s_vm.SvmGetOK{
			Payload: &models.Svm{
				State: new("running"),
				Nvme:  &models.SvmInlineNvme{Enabled: new(true)},
			},
		}, nil)
		require.NoError(t, m.validateSVMRunningState(ctx, mc.client, "uuid", "svm"))
	})

	t.Run("error when stopped", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).Return(&s_vm.SvmGetOK{
			Payload: &models.Svm{
				State: new("stopped"),
				Nvme:  &models.SvmInlineNvme{Enabled: new(true)},
			},
		}, nil)
		require.Error(t, m.validateSVMRunningState(ctx, mc.client, "uuid", "svm"))
	})

	t.Run("error when nvme disabled", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.svm.On("SvmGet", mock.Anything, mock.Anything).Return(&s_vm.SvmGetOK{
			Payload: &models.Svm{
				State: new("running"),
				Nvme:  &models.SvmInlineNvme{Enabled: new(false)},
			},
		}, nil)
		require.Error(t, m.validateSVMRunningState(ctx, mc.client, "uuid", "svm"))
	})
}

func TestCreateNetworkInterfaceForSvm(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	noBgp := &networking.NetworkIPBgpPeerGroupsGetOK{
		Payload: &models.BgpPeerGroupResponse{NumRecords: new(int64(0))},
	}

	t.Run("data lif gets nvme policy", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.networking.On("NetworkIPBgpPeerGroupsGet", mock.Anything, mock.Anything).Return(noBgp, nil)
		mc.networking.On("NetworkIPInterfacesCreate", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesCreateCreated{}, nil)

		err := m.createNetworkInterfaceForSvm(ctx, mc.client, networkInterfaceOptions{
			svmUUID: "u", svmName: "s", ipAddress: "10.0.0.1",
			lifName: "datalif+0", nodeUUID: "n", isDataLif: true,
		})
		require.NoError(t, err)

		p := mc.networking.Calls[1].Arguments[0].(*networking.NetworkIPInterfacesCreateParams)
		assert.Equal(t, "default-data-nvme-tcp", *p.Info.ServicePolicy.Name)
	})

	t.Run("mgmt lif gets management policy", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.networking.On("NetworkIPBgpPeerGroupsGet", mock.Anything, mock.Anything).Return(noBgp, nil)
		mc.networking.On("NetworkIPInterfacesCreate", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesCreateCreated{}, nil)

		err := m.createNetworkInterfaceForSvm(ctx, mc.client, networkInterfaceOptions{
			svmUUID: "u", svmName: "s", ipAddress: "10.0.0.100",
			lifName: "managementlif", nodeUUID: "n", isDataLif: false,
		})
		require.NoError(t, err)

		p := mc.networking.Calls[1].Arguments[0].(*networking.NetworkIPInterfacesCreateParams)
		assert.Equal(t, "default-management", *p.Info.ServicePolicy.Name)
	})

	t.Run("vip with /32 when bgp peers exist", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.networking.On("NetworkIPBgpPeerGroupsGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPBgpPeerGroupsGetOK{
				Payload: &models.BgpPeerGroupResponse{NumRecords: new(int64(2))},
			}, nil)
		mc.networking.On("NetworkIPInterfacesCreate", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesCreateCreated{}, nil)

		err := m.createNetworkInterfaceForSvm(ctx, mc.client, networkInterfaceOptions{
			svmUUID: "u", svmName: "s", ipAddress: "10.0.0.1",
			lifName: "datalif+0", nodeUUID: "n", isDataLif: true,
		})
		require.NoError(t, err)

		p := mc.networking.Calls[1].Arguments[0].(*networking.NetworkIPInterfacesCreateParams)
		assert.Equal(t, models.IPNetmask("32"), *p.Info.IP.Netmask)
		assert.True(t, *p.Info.Vip)
	})
}

func TestValidateAndEnsureDataLIFs(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	t.Run("no-op when all lifs exist", func(t *testing.T) {
		mc := newMockOntapClient()
		ip := models.IPAddress("10.0.0.1")
		mc.networking.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesGetOK{Payload: &models.IPInterfaceResponse{
				IPInterfaceResponseInlineRecords: []*models.IPInterface{
					{Name: new("datalif+0"), IP: &models.IPInfo{Address: &ip}},
				},
			}}, nil)

		err := m.validateAndEnsureDataLIFs(ctx, mc.client, "uuid", "svm", []string{"10.0.0.1"}, []string{"n1", "n2"})
		require.NoError(t, err)
		mc.networking.AssertNotCalled(t, "NetworkIPInterfacesCreate", mock.Anything, mock.Anything)
	})

	t.Run("creates missing lif", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.networking.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesGetOK{Payload: &models.IPInterfaceResponse{
				IPInterfaceResponseInlineRecords: []*models.IPInterface{},
			}}, nil)
		mc.networking.On("NetworkIPBgpPeerGroupsGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPBgpPeerGroupsGetOK{
				Payload: &models.BgpPeerGroupResponse{NumRecords: new(int64(0))},
			}, nil)
		mc.networking.On("NetworkIPInterfacesCreate", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesCreateCreated{}, nil)

		err := m.validateAndEnsureDataLIFs(ctx, mc.client, "uuid", "svm", []string{"10.0.0.1"}, []string{"n1", "n2"})
		require.NoError(t, err)
		mc.networking.AssertCalled(t, "NetworkIPInterfacesCreate", mock.Anything, mock.Anything)
	})
}

func TestValidateAndEnsureManagementLIF(t *testing.T) {
	ctx := context.Background()
	m := NewSvmManager(logr.Discard(), nil, nil)

	t.Run("no-op when exists with correct ip", func(t *testing.T) {
		mc := newMockOntapClient()
		ip := models.IPAddress("10.0.0.100")
		mc.networking.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesGetOK{Payload: &models.IPInterfaceResponse{
				IPInterfaceResponseInlineRecords: []*models.IPInterface{
					{Name: new("managementlif"), IP: &models.IPInfo{Address: &ip}},
				},
			}}, nil)

		err := m.validateAndEnsureManagementLIF(ctx, mc.client, "uuid", "svm", "10.0.0.100", "n1")
		require.NoError(t, err)
		mc.networking.AssertNotCalled(t, "NetworkIPInterfacesCreate", mock.Anything, mock.Anything)
	})

	t.Run("creates when missing", func(t *testing.T) {
		mc := newMockOntapClient()
		mc.networking.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesGetOK{Payload: &models.IPInterfaceResponse{
				IPInterfaceResponseInlineRecords: []*models.IPInterface{},
			}}, nil)
		mc.networking.On("NetworkIPBgpPeerGroupsGet", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPBgpPeerGroupsGetOK{
				Payload: &models.BgpPeerGroupResponse{NumRecords: new(int64(0))},
			}, nil)
		mc.networking.On("NetworkIPInterfacesCreate", mock.Anything, mock.Anything).
			Return(&networking.NetworkIPInterfacesCreateCreated{}, nil)

		err := m.validateAndEnsureManagementLIF(ctx, mc.client, "uuid", "svm", "10.0.0.100", "n1")
		require.NoError(t, err)
		mc.networking.AssertCalled(t, "NetworkIPInterfacesCreate", mock.Anything, mock.Anything)
	})
}
