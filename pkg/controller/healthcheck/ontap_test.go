package healthcheck

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gardener/gardener/extensions/pkg/controller/healthcheck"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

func TestONTAPHealthCheckerCheck(t *testing.T) {
	checker := &ontapHealthChecker{
		logger: logr.Discard(),
		config: config.ControllerConfiguration{Clusters: []config.Cluster{
			{Name: "cluster-a"},
		}},
	}

	result, err := checker.Check(t.Context(), types.NamespacedName{})
	require.NoError(t, err)
	require.Equal(t, gardencorev1beta1.ConditionFalse, result.Status)
	require.Contains(t, result.Detail, "invalid ontap api url")
}

func TestONTAPHealthCheckerCheckHealthy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cluster" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/hal+json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	checker := &ontapHealthChecker{
		logger: logr.Discard(),
		config: config.ControllerConfiguration{Clusters: []config.Cluster{
			{Name: "cluster-a", IPAddress: serverURL.Host, Username: "admin", Password: "password"},
		}},
	}

	result, err := checker.Check(t.Context(), types.NamespacedName{})
	require.NoError(t, err)
	require.Equal(t, gardencorev1beta1.ConditionTrue, result.Status)
}

var _ healthcheck.HealthCheck = &ontapHealthChecker{}
