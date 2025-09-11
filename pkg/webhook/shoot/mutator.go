package shoot

import (
	"context"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
	if new == nil {
		return nil
	}

	gvk := new.GetObjectKind().GroupVersionKind()

	if gvk.Kind == "CustomResourceDefinition" {
		crd, ok := new.(*apiextensionsv1.CustomResourceDefinition)
		if !ok {
			return nil
		}
		if crd.Spec.Group != "trident.netapp.io" {
			return nil
		}

		extensionswebhook.LogMutation(m.logger, gvk.Kind, new.GetNamespace(), new.GetName())
		return m.mutateObjectLabels(ctx, new)
	}

	if gvk.Kind == "DaemonSet" {
		daemonset, ok := new.(*appsv1.DaemonSet)
		if !ok {
			return nil
		}
		if daemonset.Name != "trident-node-linux" || daemonset.Namespace != "kube-system" {
			return nil
		}

		extensionswebhook.LogMutation(m.logger, gvk.Kind, new.GetNamespace(), new.GetName())
		return m.mutateTridentNodeDaemonSet(ctx, daemonset)
	}

	return nil
}

// mutateObjectLabels adds labels to the given object
func (m *mutator) mutateObjectLabels(_ context.Context, obj client.Object) error {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["shoot.gardener.cloud/no-cleanup"] = "true"
	labels["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"
	obj.SetLabels(labels)
	return nil
}

// mutateTridentNodeDaemonSet adds the CSI node readiness annotation to Trident node DaemonSet pods
func (m *mutator) mutateTridentNodeDaemonSet(_ context.Context, daemonset *appsv1.DaemonSet) error {
	labels := daemonset.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["node.gardener.cloud/critical-component"] = "true"
	labels["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"
	daemonset.SetLabels(labels)

	annotations := daemonset.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["node.gardener.cloud/wait-for-csi-node-ontap"] = "csi.trident.netapp.io"
	daemonset.SetAnnotations(annotations)

	return nil
}
