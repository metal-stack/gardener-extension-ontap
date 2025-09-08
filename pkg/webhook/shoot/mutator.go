package shoot

import (
	"context"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"

	"github.com/go-logr/logr"
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
	if gvk.Kind != "CustomResourceDefinition" {
		return nil
	}

	crd, ok := new.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil
	}
	if crd.Spec.Group != "trident.netapp.io" {
		return nil
	}

	extensionswebhook.LogMutation(m.logger, gvk.Kind, new.GetNamespace(), new.GetName())
	return m.mutateObjectAnnotations(ctx, new)
}

// mutateObjectAnnotations adds annotations to the given object
func (m *mutator) mutateObjectAnnotations(_ context.Context, obj client.Object) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["shoot.gardener.cloud/no-cleanup"] = "true"
	annotations["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"
	obj.SetAnnotations(annotations)
	return nil
}
