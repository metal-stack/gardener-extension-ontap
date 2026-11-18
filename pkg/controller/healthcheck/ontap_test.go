package healthcheck

import (
	"context"
	"fmt"
	"testing"

	"github.com/gardener/gardener/extensions/pkg/controller/healthcheck"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

func TestONTAPHealthCheckerCheck(t *testing.T) {
	tests := []struct {
		name       string
		checkFunc  func(context.Context, logr.Logger, config.ControllerConfiguration) error
		wantStatus gardencorev1beta1.ConditionStatus
		wantDetail string
	}{
		{
			name: "healthy",
			checkFunc: func(context.Context, logr.Logger, config.ControllerConfiguration) error {
				return nil
			},
			wantStatus: gardencorev1beta1.ConditionTrue,
		},
		{
			name: "unhealthy",
			checkFunc: func(context.Context, logr.Logger, config.ControllerConfiguration) error {
				return fmt.Errorf("cluster-a is unreachable")
			},
			wantStatus: gardencorev1beta1.ConditionFalse,
			wantDetail: "cluster-a is unreachable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &ontapHealthChecker{
				logger:    logr.Discard(),
				checkFunc: tt.checkFunc,
			}

			result, err := checker.Check(context.Background(), types.NamespacedName{})
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, result.Status)
			require.Contains(t, result.Detail, tt.wantDetail)
		})
	}
}

var _ healthcheck.HealthCheck = &ontapHealthChecker{}
