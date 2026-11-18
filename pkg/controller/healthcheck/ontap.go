package healthcheck

import (
	"context"
	"fmt"
	"time"

	"github.com/gardener/gardener/extensions/pkg/controller/healthcheck"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-ontap/pkg/ontap"
)

const ontapHealthCheckTimeout = 30 * time.Second

type ontapHealthChecker struct {
	logger logr.Logger
	config config.ControllerConfiguration
}

// NewONTAPHealthCheck returns a health check for ONTAP cluster connectivity.
func NewONTAPHealthCheck(config config.ControllerConfiguration) healthcheck.HealthCheck {
	return &ontapHealthChecker{
		config: config,
	}
}

func (h *ontapHealthChecker) SetLoggerSuffix(provider, extension string) {
	h.logger = log.Log.WithName(fmt.Sprintf("%s-%s-healthcheck-ontap", provider, extension))
}

func (h *ontapHealthChecker) Check(ctx context.Context, _ types.NamespacedName) (*healthcheck.SingleCheckResult, error) {
	checkCtx, cancel := context.WithTimeout(ctx, ontapHealthCheckTimeout)
	defer cancel()

	clients, err := ontap.NewClients(h.config)
	if err != nil {
		return &healthcheck.SingleCheckResult{
			Status: gardencorev1beta1.ConditionFalse,
			Detail: err.Error(),
		}, nil
	}

	if err := ontap.CheckConnectivity(checkCtx, clients); err != nil {
		h.logger.Error(err, "ONTAP health check failed")
		return &healthcheck.SingleCheckResult{
			Status: gardencorev1beta1.ConditionFalse,
			Detail: err.Error(),
		}, nil
	}

	return &healthcheck.SingleCheckResult{
		Status: gardencorev1beta1.ConditionTrue,
	}, nil
}
