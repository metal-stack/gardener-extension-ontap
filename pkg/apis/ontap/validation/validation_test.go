package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
)

func TestValidateTridentConfig(t *testing.T) {
	tests := []struct {
		name       string
		config     *ontapv1alpha1.TridentConfig
		wantFields []string
	}{
		{
			name: "valid",
			config: &ontapv1alpha1.TridentConfig{SvmIpaddresses: ontapv1alpha1.SvmIpaddresses{
				ManagementLif: "192.168.10.11",
				DataLifs:      []string{"192.168.10.12", "192.168.10.13"},
			}},
		},
		{
			name:       "nil config",
			config:     nil,
			wantFields: []string{"[]"},
		},
		{
			name:       "missing management lif",
			config:     &ontapv1alpha1.TridentConfig{},
			wantFields: []string{"svmIpaddresses.managementLif", "svmIpaddresses.dataLifs"},
		},
		{
			name: "invalid management lif",
			config: &ontapv1alpha1.TridentConfig{SvmIpaddresses: ontapv1alpha1.SvmIpaddresses{
				ManagementLif: "not-an-ip",
				DataLifs:      []string{"192.168.10.12"},
			}},
			wantFields: []string{"svmIpaddresses.managementLif"},
		},
		{
			name: "missing data lifs",
			config: &ontapv1alpha1.TridentConfig{SvmIpaddresses: ontapv1alpha1.SvmIpaddresses{
				ManagementLif: "192.168.10.11",
			}},
			wantFields: []string{"svmIpaddresses.dataLifs"},
		},
		{
			name: "empty data lif",
			config: &ontapv1alpha1.TridentConfig{SvmIpaddresses: ontapv1alpha1.SvmIpaddresses{
				ManagementLif: "192.168.10.11",
				DataLifs:      []string{""},
			}},
			wantFields: []string{"svmIpaddresses.dataLifs[0]"},
		},
		{
			name: "invalid data lif",
			config: &ontapv1alpha1.TridentConfig{SvmIpaddresses: ontapv1alpha1.SvmIpaddresses{
				ManagementLif: "192.168.10.11",
				DataLifs:      []string{"not-an-ip"},
			}},
			wantFields: []string{"svmIpaddresses.dataLifs[0]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateTridentConfig(tt.config)
			require.Len(t, errs, len(tt.wantFields))
			for i, wantField := range tt.wantFields {
				assert.Equal(t, wantField, errs[i].Field)
			}
		})
	}
}
