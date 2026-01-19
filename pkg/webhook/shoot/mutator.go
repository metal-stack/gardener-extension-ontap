package shoot

import (
	"context"
	"fmt"
	"strconv"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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

const mutatedByOntap = "ontap.extensions.gardener.cloud/mutated-by-webhook"

// NewMutator creates a new Mutator that mutates resources in the shoot cluster.
func NewMutator(mgr manager.Manager) extensionswebhook.Mutator {
	return &mutator{
		logger:  log.Log.WithName("shoot-mutator"),
		client:  mgr.GetClient(),
		decoder: serializer.NewCodecFactory(mgr.GetScheme(), serializer.EnableStrict).UniversalDecoder(),
	}
}

func (m *mutator) Mutate(ctx context.Context, new, _ client.Object) error {
	acc, err := meta.Accessor(new)
	if err != nil {
		return fmt.Errorf("could not create accessor during webhook %w", err)
	}

	tridentCRDs := map[string]bool{
		"tridentactionmirrorupdates.trident.netapp.io":    true,
		"tridentactionsnapshotrestores.trident.netapp.io": true,
		"tridentbackendconfigs.trident.netapp.io":         true,
		"tridentbackends.trident.netapp.io":               true,
		"tridentgroupsnapshots.trident.netapp.io":         true,
		"tridentmirrorrelationships.trident.netapp.io":    true,
		"tridentnodes.trident.netapp.io":                  true,
		"tridentsnapshotinfos.trident.netapp.io":          true,
		"tridentsnapshots.trident.netapp.io":              true,
		"tridentstorageclasses.trident.netapp.io":         true,
		"tridenttransactions.trident.netapp.io":           true,
		"tridentversions.trident.netapp.io":               true,
		"tridentvolumepublications.trident.netapp.io":     true,
		"tridentvolumereferences.trident.netapp.io":       true,
		"tridentvolumes.trident.netapp.io":                true,
	}

	// If the object does have a deletion timestamp then we don't want to mutate anything.
	if acc.GetDeletionTimestamp() != nil {
		return nil
	}

	switch x := new.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		if tridentCRDs[x.Name] {
			extensionswebhook.LogMutation(m.logger, x.Kind, x.Namespace, x.Name)
			return m.mutateObjectLabels(ctx, x.Labels, false)
		}
	case *appsv1.DaemonSet:
		if x.Name != "trident-node-linux" || x.Namespace != "kube-system" {
			return nil
		}
		extensionswebhook.LogMutation(m.logger, x.Kind, new.GetNamespace(), new.GetName())
		x.Spec.Template.Spec.DNSPolicy = corev1.DNSDefault
		return m.mutateObjectLabels(ctx, x.Spec.Template.Labels, true)
	case *appsv1.Deployment:
		if x.Name != "trident-controller" || x.Namespace != "kube-system" {
			return nil
		}
		extensionswebhook.LogMutation(m.logger, x.Kind, new.GetNamespace(), new.GetName())
		return m.mutateObjectLabels(ctx, x.Spec.Template.Labels, true)
	}

	return nil
}

// mutateObjectLabels adds labels to the given object
func (m *mutator) mutateObjectLabels(_ context.Context, labels map[string]string, podLabels bool) error {
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[v1beta1constants.ShootNoCleanup] = strconv.FormatBool(true)
	labels[mutatedByOntap] = strconv.FormatBool(true)

	if podLabels {
		labels[v1beta1constants.LabelNodeCriticalComponent] = strconv.FormatBool(true)
		labels[v1beta1constants.LabelNetworkPolicyShootToAPIServer] = v1beta1constants.LabelNetworkPolicyAllowed
		labels[v1beta1constants.LabelNetworkPolicyToDNS] = v1beta1constants.LabelNetworkPolicyAllowed
	}

	return nil
}
