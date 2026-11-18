package healthcheck

import (
	"context"
	"fmt"
	"time"

	healthcheckconfig "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	"github.com/gardener/gardener/extensions/pkg/controller/healthcheck"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	ontapcontroller "github.com/metal-stack/gardener-extension-ontap/pkg/controller/ontap"
)

var (
	defaultSyncPeriod = 30 * time.Second
	DefaultAddOptions = AddOptions{
		HealthCheckDefaults: healthcheck.DefaultAddArgs{
			HealthCheckConfig: healthcheckconfig.HealthCheckConfig{SyncPeriod: metav1.Duration{Duration: defaultSyncPeriod}},
		},
	}
)

type AddOptions struct {
	ControllerConfig    config.ControllerConfiguration
	HealthCheckDefaults healthcheck.DefaultAddArgs
}

func RegisterHealthChecks(_ context.Context, mgr manager.Manager, opts AddOptions) error {
	if err := opts.ControllerConfig.Validate(); err != nil {
		return fmt.Errorf("invalid controller configuration: %w", err)
	}

	return healthcheck.DefaultRegistration(
		ontapcontroller.ExtensionType,
		extensionsv1alpha1.SchemeGroupVersion.WithKind(extensionsv1alpha1.ExtensionResource),
		func() client.ObjectList { return &extensionsv1alpha1.ExtensionList{} },
		func() extensionsv1alpha1.Object { return &extensionsv1alpha1.Extension{} },
		mgr,
		opts.HealthCheckDefaults,
		nil,
		[]healthcheck.ConditionTypeToHealthCheck{
			{
				ConditionType: string(gardencorev1beta1.ShootSystemComponentsHealthy),
				HealthCheck:   NewONTAPHealthCheck(opts.ControllerConfig),
			},
		},
		sets.New[gardencorev1beta1.ConditionType](),
	)
}

func AddToManager(ctx context.Context, mgr manager.Manager) error {
	return RegisterHealthChecks(ctx, mgr, DefaultAddOptions)
}
