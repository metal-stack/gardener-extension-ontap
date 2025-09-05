package shoot

import (
	"context"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	tridentv1 "github.com/netapp/trident/persistent_store/crd/apis/netapp/v1"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type mutator struct {
	client  client.Client
	decoder runtime.Decoder
	logger  logr.Logger
}

// NewMutator creates a new Mutator that mutates resources in the shoot cluster.
func NewMutator(mgr manager.Manager) extensionswebhook.Mutator {
	return &mutator{
		logger:  log.Log.WithName("shoot-mutator"),
		client:  mgr.GetClient(),
		decoder: serializer.NewCodecFactory(mgr.GetScheme(), serializer.EnableStrict).UniversalDecoder(),
	}
}

func (m *mutator) Mutate(ctx context.Context, new, _ client.Object) error {

	/*
		unstruct, ok := new.(*unstructured.Unstructured)
		if !ok {
			return nil
		}

		apiVersion := unstruct.GetAPIVersion()
		kind := unstruct.GetKind()

		// Check if it's a Trident resource by checking if apiVersion contains "trident.netapp.io"
		if strings.Contains(apiVersion, "trident.netapp.io") {
			extensionswebhook.LogMutation(m.logger, kind, unstruct.GetNamespace(), unstruct.GetName())
			return m.mutateTridentResourceUnstructured(ctx, unstruct)
		}

		return nil
	*/

	/*
		unstruct, ok := new.(*unstructured.Unstructured)
		if !ok {
			return nil
		}

		if strings.Contains(apiVersion, "trident.netapp.io") {
			extensionswebhook.LogMutation(m.logger, kind, unstruct.GetNamespace(), unstruct.GetName())
			return m.mutateTridentResourceUnstructured(ctx, unstruct)
		}

		return nil
	*/

	// Check if deletion is in progress
	if new.GetDeletionTimestamp() != nil {
		return nil
	}

	switch obj := new.(type) {
	case *tridentv1.TridentBackend:
		extensionswebhook.LogMutation(m.logger, "TridentBackend", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentBackendConfig:
		extensionswebhook.LogMutation(m.logger, "TridentBackendConfig", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentVolume:
		extensionswebhook.LogMutation(m.logger, "TridentVolume", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentVolumePublication:
		extensionswebhook.LogMutation(m.logger, "TridentVolumePublication", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentNode:
		extensionswebhook.LogMutation(m.logger, "TridentNode", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentSnapshot:
		extensionswebhook.LogMutation(m.logger, "TridentSnapshot", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentSnapshotInfo:
		extensionswebhook.LogMutation(m.logger, "TridentSnapshotInfo", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentStorageClass:
		extensionswebhook.LogMutation(m.logger, "TridentStorageClass", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentTransaction:
		extensionswebhook.LogMutation(m.logger, "TridentTransaction", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentVersion:
		extensionswebhook.LogMutation(m.logger, "TridentVersion", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentMirrorRelationship:
		extensionswebhook.LogMutation(m.logger, "TridentMirrorRelationship", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentActionMirrorUpdate:
		extensionswebhook.LogMutation(m.logger, "TridentActionMirrorUpdate", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentActionSnapshotRestore:
		extensionswebhook.LogMutation(m.logger, "TridentActionSnapshotRestore", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	case *tridentv1.TridentVolumeReference:
		extensionswebhook.LogMutation(m.logger, "TridentVolumeReference", obj.Namespace, obj.Name)
		return m.addAnnotations(obj)
	default:
		return nil
	}
}

// addAnnotations adds the required annotations to any client.Object
func (m *mutator) addAnnotations(obj client.Object) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations["shoot.gardener.cloud/no-cleanup"] = "true"
	annotations["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"

	obj.SetAnnotations(annotations)

	return nil
}

// Helper functions for the commented-out unstructured approaches
/*
// mutateTridentResourceUnstructured adds annotations to Trident resources (Unstructured version)
func (m *mutator) mutateTridentResourceUnstructured(_ context.Context, obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations["shoot.gardener.cloud/no-cleanup"] = "true"
	annotations["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"

	obj.SetAnnotations(annotations)

	return nil
}


*/
