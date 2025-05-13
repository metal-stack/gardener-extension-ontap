package services

import (
	"context"
	"errors"
	"fmt"

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
	DefaultSVMUsername = "svmAdmin"
	SecretNameFormat   = "ontap-svm-%s-credentials" ////nolint:all
)

// CreateUserAndSecretOptions holds parameters for CreateUserAndSecret
type CreateUserAndSecretOptions struct {
	ProjectID              string
	SvmSeedSecretNamespace string
	SeedClient             client.Client
	SvmUUID                string
}

// CreateONTAPUserOptions holds parameters for CreateONTAPUserForSVM
type CreateONTAPUserOptions struct {
	Username         string
	SvmName          string
	KubeSeedSecretNs string
	SvmUUID          string
}

// CreateUserAndSecret creates an svm scoped account set to vsadmin role.
func CreateUserAndSecret(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap, opts CreateUserAndSecretOptions) error {
	log.Info("Creating user for SVM", "svm", opts.ProjectID)
	secretName := fmt.Sprintf(SecretNameFormat, opts.ProjectID)
	// Create or update user with the vsadmin role
	ontapUserOpts := CreateONTAPUserOptions{
		Username:         DefaultSVMUsername,
		SvmName:          opts.ProjectID,
		KubeSeedSecretNs: opts.SvmSeedSecretNamespace,
		SvmUUID:          opts.SvmUUID,
	}
	password, err := createONTAPUserForSVM(ctx, log, opts.SeedClient, ontapClient, ontapUserOpts)
	// If the secret doesn't exist in the seed that means, this is the first shoot therefore we need to create it.
	if err != nil {
		log.Error(err, "unable to create svm user")
		if errors.Is(err, ErrSeedSecretMissing) {
			log.Info("seed Secret missing for first shoot, creating...")
			tridentSecret := buildSecret(secretName, DefaultSVMUsername, password, opts.ProjectID, opts.SvmSeedSecretNamespace)
			err := opts.SeedClient.Create(ctx, tridentSecret)
			if err != nil {
				return fmt.Errorf("creating secret in seed failed: %w", err)
			}
			return nil
		}
		return fmt.Errorf("error occurred during creation of ontap user for svm %w", err)
	}
	log.Info("User created with vsadmin role and secret deployed successfully", "projectId", opts.ProjectID, "secretName", secretName)
	return nil
}

// createONTAPUserForSVM checks if the user exists on ONTAP first, then potentially creates it.
func createONTAPUserForSVM(ctx context.Context, log logr.Logger, seedClient client.Client, ontapClient *ontapv1.Ontap, opts CreateONTAPUserOptions) (string, error) {

	log.Info("Ensuring ONTAP user for SVM", "username", opts.Username, "svm", opts.SvmName)

	// Handle case where user ALREADY EXISTS on ONTAP
	log.Info("Checking K8s secret status for existing ONTAP user", "username", opts.Username, "svm", opts.SvmName)
	passwordFromSecret, secretErr := checkIfAccountExistsForSvm(ctx, log, seedClient, opts.SvmName, opts.KubeSeedSecretNs)

	// Secret also exists and is valid.
	if errors.Is(secretErr, ErrAlreadyExists) {
		log.Info("User exists on ONTAP and K8s secret is present", "username", opts.Username, "svm", opts.SvmName)
		return passwordFromSecret, ErrAlreadyExists
	}

	// This block is only reached if userExistsOnOntap was determined to be false earlier.
	password := generateSecurePassword()

	application := "http"
	authMethod := "password"
	pwdVal := strfmt.Password(password)
	vsadminRole := "vsadmin"

	createAccountParams := security.NewAccountCreateParams()
	createAccountParams.SetInfo(&models.Account{
		Name:     pointer.Pointer(opts.Username),
		Password: &pwdVal,
		Role: &models.AccountInlineRole{
			Name: pointer.Pointer(vsadminRole),
		},
		Locked: pointer.Pointer(false),
		Owner: &models.AccountInlineOwner{
			UUID: pointer.Pointer(opts.SvmUUID),
		},
		AccountInlineApplications: []*models.AccountApplication{
			{
				Application: &application,
				AuthenticationMethods: []*string{
					&authMethod,
				},
			},
		},
	})

	_, createErr := ontapClient.Security.AccountCreate(createAccountParams, nil)
	if createErr != nil {
		return "", fmt.Errorf("failed to create ONTAP user '%s' for SVM '%s': %w", opts.Username, opts.SvmName, createErr)
	}

	log.Info("ONTAP user created successfully", "username", opts.Username, "svm", opts.SvmName, "role", vsadminRole)
	return password, ErrSeedSecretMissing
}

// DeployTridentSecretsOptions holds parameters for DeployTridentSecretsInShootAsMR
type DeployTridentSecretsOptions struct {
	ProjectID      string
	ShootNamespace string
	SecretName     string
	UserName       string
	Password       strfmt.Password
}

func checkIfAccountExistsForSvm(ctx context.Context, log logr.Logger, seedClient client.Client, svmName string, kubeSeedSecret string) (string, error) {
	// Check if secret exists in the kube-system namespace in seed
	secretName := fmt.Sprintf(SecretNameFormat, svmName)
	existingSecret := &corev1.Secret{}
	err := seedClient.Get(ctx, client.ObjectKey{Namespace: kubeSeedSecret, Name: secretName}, existingSecret)
	if err != nil {
		// If secret is missing in seed
		if apierrors.IsNotFound(err) {
			log.Info("Secret not found in seed", "secretName", secretName, "namespace", kubeSeedSecret)
			return "", ErrSeedSecretMissing
		}
		log.Error(err, "Failed to get secret from seed", "secretName", secretName, "namespace", kubeSeedSecret)
		return "", fmt.Errorf("failed to get secret %s from namespace %s: %w", secretName, kubeSeedSecret, err)
	}
	// Secret exists, check if password field is present and not empty
	if password, ok := existingSecret.Data["password"]; ok && len(password) > 0 {
		log.Info("Secret exists and contains a password", "secretName", secretName, "namespace", kubeSeedSecret)
		return string(password), ErrAlreadyExists
	}
	log.Info("Secret exists but password field is missing or empty, considering it missing", "secretName", secretName, "namespace", kubeSeedSecret)
	return "", ErrSeedSecretMissing
}

// very secure password for now
// FIXME what is this for
func generateSecurePassword() string {
	return "fsqe2020"
}

// deployTridentSecrets creates or updates the secret for Trident
func DeployTridentSecretsInShootAsMR(ctx context.Context, log logr.Logger, seedClient client.Client, opts DeployTridentSecretsOptions) error {

	// Create the secret in the shoot namespace instead of kube-system
	tridentSecret := buildSecret(opts.SecretName, opts.UserName, string(opts.Password), opts.ProjectID, "kube-system")
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
		opts.ShootNamespace,
		"trident-credentials",
		"kube-system",
		false,
		shootResources,
	)
	if err != nil {
		return fmt.Errorf("failed to create ManagedResource for credentials: %w", err)
	}
	log.Info("Trident credentials secret created and confirmed healthy",
		"projectId", opts.ProjectID,
		"shootNamespace", opts.ShootNamespace)
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
