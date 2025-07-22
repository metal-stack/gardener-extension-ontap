package cwnps

import (
	_ "embed"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"
)

var expected = `apiVersion: metal-stack.io/v1
kind: ClusterwideNetworkPolicy
metadata:
  namespace: firewall
  name: allow-to-ontap
spec:
  egress:
  - to:
    cidr: "192.168.0.1/32"
    ports:
    - protocol: TCP
      port: 443
  - to:
    cidr: "192.168.0.2/32"
    ports:
    - protocol: TCP
      port: 4420
  - to:
    cidr: "192.168.0.3/32"
    ports:
    - protocol: TCP
      port: 4420
`

func TestParseCWNP(t *testing.T) {
	tests := []struct {
		name    string
		cwnp    CWNP
		want    string
		wantErr bool
	}{
		{
			name:    "simple cwnp",
			cwnp:    CWNP{ManagementLif: "192.168.0.1", DataLifs: []string{"192.168.0.2", "192.168.0.3"}},
			want:    expected,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCWNP(tt.cwnp)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCWNP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			var (
				gotRes  = map[string]any{}
				wantRes = map[string]any{}
			)
			err = yaml.Unmarshal([]byte(got), gotRes)
			if err != nil {
				t.Errorf("unable to unmarshal:%v", err)
			}
			err = yaml.Unmarshal([]byte(tt.want), wantRes)
			if err != nil {
				t.Errorf("unable to unmarshal:%v", err)
			}

			if diff := cmp.Diff(gotRes, wantRes); diff != "" {
				t.Errorf("ParseCWNP() diff %s", diff)
			}
		})
	}
}
