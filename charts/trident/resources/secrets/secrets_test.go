package secrets_test

import (
	_ "embed"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/metal-stack/gardener-extension-ontap/charts/trident/resources/secrets"
	"go.yaml.in/yaml/v3"
)

var expected = `apiVersion: v1
kind: Secret
metadata:
  name: a-secret
  namespace: kube-system
  labels:
    app.kubernetes.io/part-of: gardener-extension-ontap
    app.kubernetes.io/managed-by: gardener
    ontap.metal-stack.io/project-id: project-a
type: Opaque
stringData:
  username: a-user
  password: a-password
`

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		secret  secrets.Secrets
		want    string
		wantErr bool
	}{
		{
			name: "simple cwnp",
			secret: secrets.Secrets{
				Name:      "a-secret",
				Namespace: "kube-system",
				Project:   "project-a",
				Username:  "a-user",
				Password:  "a-password",
			},
			want:    expected,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := secrets.Parse(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			fmt.Printf("got:\n%s\n", got)
			fmt.Printf("want:\n%s\n", tt.want)

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
				t.Errorf("Parse() diff %s", diff)
			}
		})
	}
}
