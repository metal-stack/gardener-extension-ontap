package shoot

import (
	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	"github.com/gardener/gardener/extensions/pkg/webhook/shoot"
	tridentv1 "github.com/netapp/trident/persistent_store/crd/apis/netapp/v1"
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

	/*
		tridentTypes := []extensionswebhook.Type{}

		// Alle Trident CRD Types registrieren
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
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    crdKind,
			})
			tridentTypes = append(tridentTypes, extensionswebhook.Type{Obj: u})
		}

		return shoot.New(mgr, shoot.Args{
			Types:   tridentTypes,
			Mutator: NewMutator(mgr),
		})
	*/

	/*
		return shoot.New(mgr, shoot.Args{
			Types: []extensionswebhook.Type{
				{Obj: &unstructured.Unstructured{}},
			},
			Mutator: NewMutator(mgr),
		})
	*/

	// Option 3: Echte Trident Go Types (aktuell verwendet)
	return shoot.New(mgr, shoot.Args{
		Types: []extensionswebhook.Type{
			{Obj: &tridentv1.TridentBackend{}},
			{Obj: &tridentv1.TridentBackendConfig{}},
			{Obj: &tridentv1.TridentVolume{}},
			{Obj: &tridentv1.TridentVolumePublication{}},
			{Obj: &tridentv1.TridentNode{}},
			{Obj: &tridentv1.TridentSnapshot{}},
			{Obj: &tridentv1.TridentSnapshotInfo{}},
			{Obj: &tridentv1.TridentStorageClass{}},
			{Obj: &tridentv1.TridentTransaction{}},
			{Obj: &tridentv1.TridentVersion{}},
			{Obj: &tridentv1.TridentMirrorRelationship{}},
			{Obj: &tridentv1.TridentActionMirrorUpdate{}},
			{Obj: &tridentv1.TridentActionSnapshotRestore{}},
			{Obj: &tridentv1.TridentVolumeReference{}},
		},
		Mutator: NewMutator(mgr),
	})
}

// AddToManager creates a webhook with the default options and adds it to the manager.
func AddToManager(mgr manager.Manager) (*extensionswebhook.Webhook, error) {
	return AddToManagerWithOptions(mgr, DefaultAddOptions)
}
