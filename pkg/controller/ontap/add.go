package ontap

import (
	"context"

	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	runtimelog "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// Type is the type of Extension resource.
	Type = "ontap"
	// ControllerName is the name of the registry cache service controller.
	ControllerName = "ontap_controller"
	// FinalizerSuffix is the finalizer suffix for the registry cache service controller.
	FinalizerSuffix = "ontap"
)

var (
	// DefaultAddOptions are the default AddOptions for AddToManager.
	DefaultAddOptions = AddOptions{}
)
var log = runtimelog.Log.WithName("gardener-extension-ontap")

// AddOptions are options to apply when adding the registry cache service controller to the manager.
type AddOptions struct {
	// ControllerOptions contains options for the controller.
	ControllerOptions controller.Options
	// Config contains configuration for the registry cache service.
	Config config.ControllerConfiguration
	// IgnoreOperationAnnotation specifies whether to ignore the operation annotation or not.
	IgnoreOperationAnnotation bool
}

// AddToManager adds a controller with the default Options to the given Controller Manager.
func AddToManager(ctx context.Context, mgr manager.Manager) error {
	return AddToManagerWithOptions(ctx, mgr, DefaultAddOptions)
}

// AddToManagerWithOptions adds a controller with the given Options to the given manager.
// The opts.Reconciler is being set with a newly instantiated actuator.
func AddToManagerWithOptions(ctx context.Context, mgr manager.Manager, opts AddOptions) error {

	//CHANGE
	opts.Config.AdminAuthSecretRef = "admin-access"
	opts.Config.ClusterManagementIp = "test"
	opts.Config.AuthSecretNamespace = "garden"

	actuator, err := NewActuator(log, ctx, mgr, opts.Config)

	if err != nil {
		return err
	}

	return extension.Add(ctx, mgr, extension.AddArgs{
		Actuator:          actuator,
		ControllerOptions: opts.ControllerOptions,
		Name:              ControllerName,
		FinalizerSuffix:   FinalizerSuffix,
		Resync:            0,
		Predicates:        extension.DefaultPredicates(ctx, mgr, DefaultAddOptions.IgnoreOperationAnnotation),
		Type:              Type,
	})
}
