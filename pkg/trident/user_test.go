package trident

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/security"
	"github.com/metal-stack/ontap-go/api/models"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateAndEnsureCompleteUserState(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("both secret and ontap user exist - no-op", func(t *testing.T) {
		mc := newMockOntapClient()
		// ONTAP user exists
		mc.security.On("AccountCollectionGet", mock.Anything, mock.Anything).
			Return(&security.AccountCollectionGetOK{Payload: &models.AccountResponse{
				AccountResponseInlineRecords: []*models.Account{
					{Name: new("myshoot")},
				},
			}}, nil)

		// K8s secret exists with password
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "proj-1-proj--myshoot-credentials",
				Namespace: "kube-system",
			},
			Data: map[string][]byte{
				"username": []byte("myshoot"),
				"password": []byte("existing-pw"),
			},
		}
		k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingSecret).Build()

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, k8s)
		err := m.validateAndEnsureCompleteUserState(ctx, mc.client, userAndSecretOptions{
			projectID:              "proj-1",
			shootNamespace:         "shoot--proj--myshoot",
			svmSeedSecretNamespace: "kube-system",
			seedClient:             k8s,
			svmUUID:                "svm-uuid-1",
		})
		require.NoError(t, err)
	})

	t.Run("neither secret nor user exist - creates both", func(t *testing.T) {
		mc := newMockOntapClient()

		// ONTAP user does NOT exist
		mc.security.On("AccountCollectionGet", mock.Anything, mock.Anything).
			Return(&security.AccountCollectionGetOK{Payload: &models.AccountResponse{
				AccountResponseInlineRecords: []*models.Account{},
			}}, nil)

		// ONTAP user creation succeeds
		mc.security.On("AccountCreate", mock.Anything, mock.Anything).
			Return(&security.AccountCreateCreated{}, nil)

		// No pre-existing K8s secret
		k8s := fake.NewClientBuilder().WithScheme(scheme).Build()

		m := NewSvmManager(logr.Discard(), []*ontapv1.Ontap{mc.client}, k8s)
		err := m.validateAndEnsureCompleteUserState(ctx, mc.client, userAndSecretOptions{
			projectID:              "proj-1",
			shootNamespace:         "shoot--proj--myshoot",
			svmSeedSecretNamespace: "kube-system",
			seedClient:             k8s,
			svmUUID:                "svm-uuid-1",
		})
		require.NoError(t, err)

		// Verify ONTAP user was created
		mc.security.AssertCalled(t, "AccountCreate", mock.Anything, mock.Anything)
	})
}
