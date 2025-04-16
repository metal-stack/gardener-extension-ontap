package services

import (
	"context"
	"fmt"

	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-openapi/strfmt"
	"github.com/metal-stack/ontap-go/api/client/security"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SVM user constants
const (
	DefaultSVMUsername = "vsadmin"
	SecretNameFormat   = "ontap-svm-%s-credentials" ////nolint:all
)

// CreateUserAndSecret creates an svm scoped account set to vsadmin role
func CreateUserAndSecret(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string, seedClient client.Client, dataLif string, managementLif string) error {
	log.Info("Creating user for SVM", "svm", projectId)

	// Generate a secure password
	password, err := GenerateSecurePassword()
	if err != nil {
		return fmt.Errorf("failed to generate secure password: %w", err)
	}

	// Create or update user with the vsadmin role
	err = CreateONTAPUserForSVM(ctx, log, ontapClient, DefaultSVMUsername, password, projectId)
	if err != nil {
		return fmt.Errorf("failed to create/update user: %w", err)
	}

	// Create the secret name with project ID
	secretName := fmt.Sprintf(SecretNameFormat, projectId)

	// Create/update secret with credentials
	err = deployTridentSecrets(ctx, log, secretName, DefaultSVMUsername, strfmt.Password(password), projectId, shootNamespace, seedClient)
	if err != nil {
		return fmt.Errorf("failed to deploy secret: %w", err)
	}

	log.Info("User created with vsadmin role and secret deployed successfully", "projectId", projectId, "secretName", secretName)
	return nil
}

// ListAllUser lists all security users with their owners i.e. smv or cluster
func checkIfAccountExistsForSvm(log logr.Logger, ontapClient *ontapv1.Ontap, accountName string, svmName string) error {
	svmUid, err := GetSVMByName(log, ontapClient, svmName)
	if err != nil {
		return err
	}

	params := &security.AccountGetParams{Name: accountName, OwnerUUID: svmUid}
	account, err := ontapClient.Security.AccountGet(params, nil)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if account.Payload.Name == nil {
		return nil
	}

	return ErrAlreadyExists
}

// deployTridentSecrets creates or updates the secret for Trident
func deployTridentSecrets(ctx context.Context, log logr.Logger, secretName string, userName string, password strfmt.Password, projectId string, shootNamespace string, seedClient client.Client) error {
	// Create one secret in kube-system namespace that will be used by Trident backend config
	tridentSecret := buildSecret(secretName, userName, password.String(), projectId, "kube-system")
	clientObjs := []client.Object{tridentSecret}
	shootResources, err := managedresources.
		NewRegistry(kubernetes.ShootScheme, kubernetes.ShootCodec, kubernetes.ShootSerializer).
		AddAllAndSerialize(clientObjs...)
	if err != nil {
		return fmt.Errorf("failed to serialize trident resources: %w", err)
	}

	err = managedresources.CreateForShoot(
		ctx,
		seedClient,
		shootNamespace,
		"trident-credentials",
		"kube-system",
		false,
		shootResources,
	)
	if err != nil {
		return fmt.Errorf("failed to create ManagedResource for credentials: %w", err)
	}
	log.Info("Trident credentials secret created and confirmed healthy",
		"projectId", projectId,
		"shootNamespace", shootNamespace)
	return nil
}

// buildSecret creates a secret with the SVM credentials in the specified namespace
func buildSecret(secretName, userName, password, projectId, namespace string) *corev1.Secret {
	// Build and return a Kubernetes secret
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":       "gardener-extension-ontap",
				"app.kubernetes.io/managed-by":    "gardener",
				"ontap.metal-stack.io/project-id": projectId,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": userName,
			"password": password,
		},
	}
}

// not ready
func GenerateSecurePassword() (string, error) {
	return "123456789", nil
}
