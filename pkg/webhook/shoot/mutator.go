package shoot

import (
	"context"
	"fmt"
	"slices"
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

const (
	mutatedByOntap = "ontap.extensions.gardener.cloud/mutated-by-webhook"

	// tridentAPIGroup is the API group of all Trident custom resources.
	tridentAPIGroup = "trident.netapp.io"
)

var tridentCRDsManagedByExtension = []string{
	"tridentconfigurators." + tridentAPIGroup,
	"tridentorchestrators." + tridentAPIGroup,
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
	_, err := meta.Accessor(new)
	if err != nil {
		return fmt.Errorf("could not create accessor during webhook %w", err)
	}

	switch x := new.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		if x.Spec.Group != tridentAPIGroup || slices.Contains(tridentCRDsManagedByExtension, x.Name) {
			return nil
		}
		extensionswebhook.LogMutation(m.logger, x.Kind, x.Namespace, x.Name)
		x.Labels = mutateObjectLabels(x.Labels, false)
	case *appsv1.DaemonSet:
		if x.Name != "trident-node-linux" || x.Namespace != "kube-system" {
			return nil
		}
		extensionswebhook.LogMutation(m.logger, x.Kind, new.GetNamespace(), new.GetName())
		x.Spec.Template.Spec.DNSPolicy = corev1.DNSDefault

		if !slices.ContainsFunc(x.Spec.Template.Spec.InitContainers, func(c corev1.Container) bool {
			return c.Name == "init-nvme-tcp"
		}) {
			propagation := corev1.MountPropagationHostToContainer
			privileged := true
			x.Spec.Template.Spec.InitContainers = append(x.Spec.Template.Spec.InitContainers, corev1.Container{
				Name:  "init-nvme-tcp",
				Image: "busybox:1.36",
				SecurityContext: &corev1.SecurityContext{
					Privileged: &privileged,
				},
				Command: []string{"/bin/sh", "-c", "[ -e /sys/module/nvme-tcp ] && modinfo nvme-tcp || { modinfo nvme-tcp && modprobe nvme-tcp ; } || { echo \"FAILED to load nvme-tcp kernel driver\" && exit 1 ; }"},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:             "modules-dir",
						MountPath:        "/lib/modules",
						MountPropagation: &propagation,
					},
				},
			})
		}

		if !slices.ContainsFunc(x.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool {
			return v.Name == "modules-dir"
		}) {
			x.Spec.Template.Spec.Volumes = append(x.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: "modules-dir",
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/lib/modules",
				},
			})
		}

		if x.Spec.Template.Annotations == nil {
			x.Spec.Template.Annotations = make(map[string]string)
		}
		x.Spec.Template.Annotations["node.gardener.cloud/wait-for-csi-node-trident"] = "csi.trident.netapp.io"

		x.Spec.Template.Labels = mutateObjectLabels(x.Spec.Template.Labels, true)
		return nil
	case *appsv1.Deployment:
		if x.Name != "trident-controller" || x.Namespace != "kube-system" {
			return nil
		}
		extensionswebhook.LogMutation(m.logger, x.Kind, new.GetNamespace(), new.GetName())
		x.Spec.Template.Labels = mutateObjectLabels(x.Spec.Template.Labels, true)
		return nil
	}

	return nil
}

// mutateObjectLabels adds labels to the given labels map and returns it.
func mutateObjectLabels(labels map[string]string, podLabels bool) map[string]string {
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

	return labels
}
