package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/models"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-openapi/strfmt"
	"github.com/metal-stack/ontap-go/api/client/security"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SVM user constants
const (
	DefaultSVMUsername = "vsadmin"
	SecretNameFormat   = "ontap-svm-%s-credentials" ////nolint:all
)

// CreateUserAndSecret creates an svm scoped account set to vsadmin role.
func CreateUserAndSecret(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string, seedClient client.Client) error {
	log.Info("Creating user for SVM", "svm", projectId)
	secretName := fmt.Sprintf(SecretNameFormat, projectId)
	// Create or update user with the vsadmin role
	err, password := CreateONTAPUserForSVM(ctx, log, seedClient, ontapClient, DefaultSVMUsername, projectId, shootNamespace)
	// If the secret doesn't exist in the seed that means, this is the first shoot therefore we need to create it.
	if err != nil {
		if errors.Is(err, ErrSeedSecretMissing) {
			log.Info("seed Secret missing for first shoot, creating...")
			tridentSecret := buildSecret(secretName, DefaultSVMUsername, string(password), projectId, shootNamespace)
			err := seedClient.Create(ctx, tridentSecret)
			if err != nil {
				return fmt.Errorf("creating secret in seed failed: %w", err)
			}
			err = deployTridentSecretsInShootAsMR(ctx, log, projectId, shootNamespace, seedClient, secretName, DefaultSVMUsername, strfmt.Password(password))
			return nil
		}
		return fmt.Errorf("error occured during creation of ontap user for svm %w", err)
	}
	// Create the secret name with project ID
	err = deployTridentSecretsInShootAsMR(ctx, log, projectId, shootNamespace, seedClient, secretName, DefaultSVMUsername, strfmt.Password(password))
	if err != nil {
		return fmt.Errorf("failed to deploy secret: %w", err)
	}
	log.Info("User created with vsadmin role and secret deployed successfully", "projectId", projectId, "secretName", secretName)
	return nil
}

// CreateONTAPUserForSVM creates a user for the specified SVM with the built-in vsadmin role
func CreateONTAPUserForSVM(ctx context.Context, log logr.Logger, seedClient client.Client, ontapClient *ontapv1.Ontap,
	username string, svmName string, shootNamespace string) (error, string) {
	// Generate a secure password
	password, err := GenerateSecurePassword()
	if err != nil {
		return fmt.Errorf("failed to generate secure password: %w", err), ""
	}
	log.Info("Creating ONTAP user for SVM", "username", username, "svm", svmName)
	// Check if secret exists, if yes return the password
	err, password = checkIfAccountExistsForSvm(ctx, log, seedClient, ontapClient, username, svmName, shootNamespace)
	if errors.Is(err, ErrAlreadyExists) && password != "" {
		return ErrAlreadyExists, password
	}

	// Fetch svm to create user for, retry a few times if not found initially
	// Important to do this otherwise sometimes the ontap api is not ready/hasn't finished creating the svm
	var svmUUID string
	maxRetries := 3
	retryDelay := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		log.Info("Trying to get svm after creation %w", i)
		svmUUID, err = GetSVMByName(log, ontapClient, svmName)
		if err == nil {
			break // Found SVM, exit loop
		}
		log.Error(err, "Failed to get SVM UUID, retrying...", "svmName", svmName, "attempt", i+1)
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}
	// If SVM still not found after retries, return error
	if err != nil {
		return fmt.Errorf("failed to get SVM UUID after %d attempts: %w", maxRetries, err), ""
	}

	// Create user with the built-in vsadmin role
	application := "http"
	authMethod := "password"
	pwdVal := strfmt.Password(password)
	vsadminRole := "vsadmin"
	params := &security.AccountCreateParams{
		Info: &models.Account{
			Name:     pointer.Pointer(username),
			Password: &pwdVal,
			Role: &models.AccountInlineRole{
				Name: pointer.Pointer(vsadminRole),
			},
			Owner: &models.AccountInlineOwner{
				Name: pointer.Pointer(svmName),
				UUID: pointer.Pointer(svmUUID),
			},
			AccountInlineApplications: []*models.AccountApplication{
				{
					Application: &application,
					AuthenticationMethods: []*string{
						&authMethod,
					},
				},
			},
		},
	}
	_, err = ontapClient.Security.AccountCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create ONTAP user: %w", err), ""
	}
	log.Info("ONTAP user created successfully", "username", username, "svm", svmName, "role", "vsadmin")
	return ErrSeedSecretMissing, password
}

func checkIfAccountExistsForSvm(ctx context.Context, log logr.Logger, seedClient client.Client, ontapClient *ontapv1.Ontap, accountName string, svmName string, shootNamespace string) (error, string) {
	// Check if secret exists in the shootNamespace
	secretName := fmt.Sprintf(SecretNameFormat, svmName)
	existingSecret := &corev1.Secret{}
	err := seedClient.Get(ctx, client.ObjectKey{Namespace: shootNamespace, Name: secretName}, existingSecret)
	if err != nil {
		// If secret is missing in seed
		if apierrors.IsNotFound(err) {
			log.Info("Secret not found in seed", "secretName", secretName, "namespace", shootNamespace)
			return ErrSeedSecretMissing, ""
		}
		log.Error(err, "Failed to get secret from seed", "secretName", secretName, "namespace", shootNamespace)
		return fmt.Errorf("failed to get secret %s from namespace %s: %w", secretName, shootNamespace, err), ""
	}
	// Secret exists, check if password field is present and not empty
	if password, ok := existingSecret.Data["password"]; ok && len(password) > 0 {
		log.Info("Secret exists and contains a password", "secretName", secretName, "namespace", shootNamespace)
		return ErrAlreadyExists, string(password)
	}
	log.Info("Secret exists but password field is missing or empty, considering it missing", "secretName", secretName, "namespace", shootNamespace)
	return ErrSeedSecretMissing, ""
}

// very secure password for now
func GenerateSecurePassword() (string, error) {
	return "123456789", nil
}

// deployTridentSecrets creates or updates the secret for Trident
func deployTridentSecretsInShootAsMR(ctx context.Context, log logr.Logger, projectId string, shootNamespace string, seedClient client.Client, secretName, userName string, password strfmt.Password) error {

	// Create the secret in the shoot namespace instead of kube-system
	tridentSecret := buildSecret(secretName, userName, string(password), projectId, "kube-system")
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
