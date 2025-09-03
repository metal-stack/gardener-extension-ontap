package shoot

import (
	"context"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	// Check for deletion timestamp
	if new.GetDeletionTimestamp() != nil {
		return nil
	}

	switch x := new.(type) {
	case *appsv1.Deployment:
		extensionswebhook.LogMutation(m.logger, "Deployment", x.Namespace, x.Name)
		return m.mutateDeployment(ctx, x)
	case *corev1.Pod:
		extensionswebhook.LogMutation(m.logger, "Pod", x.Namespace, x.Name)
		return m.mutatePod(ctx, x)
	}
	return nil
}

func (m *mutator) mutateDeployment(_ context.Context, deployment *appsv1.Deployment) error {
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}

	deployment.Annotations["shoot.gardener.cloud/no-cleanup"] = "true"
	deployment.Annotations["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"
	
	return nil
}

func (m *mutator) mutatePod(_ context.Context, pod *corev1.Pod) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	pod.Annotations["shoot.gardener.cloud/no-cleanup"] = "true"
	pod.Annotations["ontap.extensions.gardener.cloud/mutated-by-webhook"] = "true"
	
	return nil
}
