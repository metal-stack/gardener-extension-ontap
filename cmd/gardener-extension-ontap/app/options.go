package app

import (
	"context"
	"fmt"
	"os"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/install"
	"github.com/metal-stack/metal-lib/pkg/pointer"

	corev1 "k8s.io/api/core/v1"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	heartbeatcontroller "github.com/gardener/gardener/extensions/pkg/controller/heartbeat"
	heartbeatcmd "github.com/gardener/gardener/extensions/pkg/controller/heartbeat/cmd"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	ontapcmd "github.com/metal-stack/gardener-extension-ontap/pkg/cmd"
	controller "github.com/metal-stack/gardener-extension-ontap/pkg/controller/ontap"

	controllercmd "github.com/gardener/gardener/extensions/pkg/controller/cmd"
	"github.com/gardener/gardener/extensions/pkg/util"
	ghealth "github.com/gardener/gardener/pkg/healthz"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// var log = runtimelog.Log.WithName("gardener-extension-ontap")

const ExtensionName = "extension-ontap"

type Options struct {
	generalOptions     *controllercmd.GeneralOptions
	ontapOptions       *ontapcmd.AuthOptions
	restOptions        *controllercmd.RESTOptions
	managerOptions     *controllercmd.ManagerOptions
	controllerOptions  *controllercmd.ControllerOptions
	heartbeatOptions   *heartbeatcmd.Options
	healthOptions      *controllercmd.ControllerOptions
	controllerSwitches *controllercmd.SwitchOptions
	reconcileOptions   *controllercmd.ReconcilerOptions
	optionAggregator   controllercmd.OptionAggregator
}

func NewOptions() *Options {
	options := &Options{
		generalOptions: &controllercmd.GeneralOptions{},
		ontapOptions:   &ontapcmd.AuthOptions{},
		restOptions:    &controllercmd.RESTOptions{},
		managerOptions: &controllercmd.ManagerOptions{
			LeaderElection:          true,
			LeaderElectionID:        controllercmd.LeaderElectionNameID(ExtensionName),
			LeaderElectionNamespace: os.Getenv("LEADER_ELECTION_NAMESPACE"),
			MetricsBindAddress:      ":8080",
			HealthBindAddress:       ":8081",
		},

		// options for the controlplane controller
		controllerOptions: &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		},

		heartbeatOptions: &heartbeatcmd.Options{
			// This is a default value.
			ExtensionName:        ExtensionName,
			RenewIntervalSeconds: 30,
			Namespace:            os.Getenv("LEADER_ELECTION_NAMESPACE"),
		},
		healthOptions: &controllercmd.ControllerOptions{
			// This is a default value.
			MaxConcurrentReconciles: 5,
		},

		//Throws Panic
		controllerSwitches: ontapcmd.ControllerSwitchOptions(),
		reconcileOptions:   &controllercmd.ReconcilerOptions{IgnoreOperationAnnotation: true},
	}

	options.optionAggregator = controllercmd.NewOptionAggregator(
		options.generalOptions,
		options.ontapOptions,
		options.restOptions,
		options.managerOptions,
		options.controllerOptions,
		controllercmd.PrefixOption("heartbeat-", options.heartbeatOptions),
		controllercmd.PrefixOption("healthcheck-", options.healthOptions),
		options.controllerSwitches,
		options.reconcileOptions,
	)

	return options
}

func (options *Options) run(ctx context.Context) error {
	log.Info("starting extension", "ex", ExtensionName)

	util.ApplyClientConnectionConfigurationToRESTConfig(&componentbaseconfig.ClientConnectionConfiguration{
		QPS:   100.0,
		Burst: 130,
	}, options.restOptions.Completed().Config)

	log.Info("applied rest config")

	mgrOpts := options.managerOptions.Completed().Options()

	log.Info("completed mgr-options")

	mgrOpts.Client = client.Options{
		Cache: &client.CacheOptions{
			DisableFor: []client.Object{
				&corev1.Secret{},
				&corev1.ConfigMap{},
			},
		},
	}

	mgr, err := manager.New(options.restOptions.Completed().Config, mgrOpts)
	if err != nil {
		return fmt.Errorf("could not instantiate controller-manager: %w", err)
	}
	log.Info("completed rest-options")

	err = extensionscontroller.AddToScheme(mgr.GetScheme())
	if err != nil {
		return fmt.Errorf("could not add mgr-scheme to extension-controller: %w", err)
	}
	log.Info("added mgr-scheme to extensionscontroller")

	err = install.AddToScheme(mgr.GetScheme())
	if err != nil {
		return fmt.Errorf("could not add mgr-scheme to installation")
	}
	log.Info("added mgr-scheme to installation")

	options.controllerOptions.Completed().Apply(&controller.DefaultAddOptions.ControllerOptions)
	options.reconcileOptions.Completed().Apply(&controller.DefaultAddOptions.IgnoreOperationAnnotation, pointer.Pointer(extensionsv1alpha1.ExtensionClassShoot))
	options.heartbeatOptions.Completed().Apply(&heartbeatcontroller.DefaultAddOptions)

	if err := options.controllerSwitches.Completed().AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("could not add controllers to manager: %w", err)
	}
	log.Info("added controllers to manager")

	if err := mgr.AddReadyzCheck("informer-sync", ghealth.NewCacheSyncHealthz(mgr.GetCache())); err != nil {
		return fmt.Errorf("could not add ready check for informers: %w", err)
	}
	log.Info("added readyzcheck")

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("could not add health check to manager: %w", err)
	}
	log.Info("added healthzcheck")

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("error running manager: %w", err)
	}

	return nil
}
