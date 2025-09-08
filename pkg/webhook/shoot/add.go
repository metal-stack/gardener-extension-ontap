package shoot

import (
	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	"github.com/gardener/gardener/extensions/pkg/webhook/shoot"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var (
	// DefaultAddOptions are the default AddOptions for AddToManager.
	DefaultAddOptions = AddOptions{}
)

// AddOptions are options to apply when adding the ONTAP shoot webhook to the manager.
type AddOptions struct{}

var logger = log.Log.WithName("ontap-shoot-webhook")

// AddToManagerWithOptions creates a webhook with the given options and adds it to the manager.
func AddToManagerWithOptions(mgr manager.Manager, opts AddOptions) (*extensionswebhook.Webhook, error) {
	logger.Info("Adding webhook to manager")

	var tridentTypes []*apiextensionsv1.CustomResourceDefinition
	tridentCRDs := []string{
		"TridentOrchestrator",
		"TridentBackend",
		"TridentBackendConfig",
		"TridentMirrorRelationship",
		"TridentActionMirrorUpdate",
		"TridentActionSnapshotRestore",
		"TridentSnapshot",
		"TridentSnapshotInfo",
		"TridentStorageClass",
		"TridentVolume",
		"TridentVolumePublication",
		"TridentVolumeReference",
		"TridentNode",
		"TridentTransaction",
		"TridentVersion",
	}

	for _, crdKind := range tridentCRDs {
		tridentCRD := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdKind,
			},
		}
		tridentTypes = append(tridentTypes, tridentCRD)
	}

	var types []extensionswebhook.Type
	for _, crd := range tridentTypes {
		types = append(types, extensionswebhook.Type{Obj: crd})
	}

	return shoot.New(mgr, shoot.Args{
		Types:   types,
		Mutator: NewMutator(mgr),
	})

}

// AddToManager creates a webhook with the default options and adds it to the manager.
func AddToManager(mgr manager.Manager) (*extensionswebhook.Webhook, error) {
	return AddToManagerWithOptions(mgr, DefaultAddOptions)
}
