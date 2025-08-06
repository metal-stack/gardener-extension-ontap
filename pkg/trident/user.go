package trident

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sethvargo/go-password/password"

	"github.com/metal-stack/metal-lib/pkg/pointer"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/models"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/metal-stack/ontap-go/api/client/security"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SVM user constants
const (
	defaultSVMUsername = "svmAdmin"
	SecretNameFormat   = "ontap-svm-%s-credentials" ////nolint:all
)

// DeployTridentSecretsOptions holds parameters for DeployTridentSecretsInShootAsMR
type DeployTridentSecretsOptions struct {
	ProjectID      string
	ShootNamespace string
	SecretName     string
	UserName       string
	Password       strfmt.Password
}

// userAndSecretOptions holds parameters for CreateUserAndSecret
type userAndSecretOptions struct {
	projectID              string
	svmSeedSecretNamespace string
	seedClient             client.Client
	svmUUID                string
}

// ontapUserOptions holds parameters for CreateONTAPUserForSVM
type ontapUserOptions struct {
	username         string
	svmName          string
	kubeSeedSecretNs string
	svmUUID          string
}

// CreateUserAndSecret creates an svm scoped account set to vsadmin role.
func (m *SvmManager) CreateUserAndSecret(ctx context.Context, opts userAndSecretOptions) error {
	m.log.Info("Creating user for SVM", "svm", opts.projectID)
	secretName := fmt.Sprintf(SecretNameFormat, opts.projectID)
	// Create or update user with the vsadmin role
	ontapUserOpts := ontapUserOptions{
		username:         defaultSVMUsername,
		svmName:          opts.projectID,
		kubeSeedSecretNs: opts.svmSeedSecretNamespace,
		svmUUID:          opts.svmUUID,
	}
	password, err := m.createONTAPUserForSVM(ctx, ontapUserOpts)
	// If the secret doesn't exist in the seed that means, this is the first shoot therefore we need to create it.
	if err != nil {
		m.log.Error(err, "unable to create svm user")
		if errors.Is(err, ErrSeedSecretMissing) {
			m.log.Info("seed Secret missing for first shoot, creating...")
			err = m.buildAndCreateSecretInSeed(ctx, secretName, defaultSVMUsername, password, opts.projectID)
			if err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("error occurred during creation of ontap user for svm %w", err)
	}
	m.log.Info("User created with vsadmin role and secret deployed successfully", "projectId", opts.projectID, "secretName", secretName)
	return nil
}

// createONTAPUserForSVM checks if the user exists on ONTAP first, then potentially creates it.
func (m *SvmManager) createONTAPUserForSVM(ctx context.Context, opts ontapUserOptions) (string, error) {

	m.log.Info("Ensuring ONTAP user for SVM", "username", opts.username, "svm", opts.svmName)

	// Handle case where user ALREADY EXISTS on ONTAP
	m.log.Info("Checking K8s secret status for existing ONTAP user", "username", opts.username, "svm", opts.svmName)
	passwordFromSecret, secretErr := m.checkIfAccountExistsForSvm(ctx, opts.svmName, opts.kubeSeedSecretNs)

	// Secret also exists and is valid.
	if errors.Is(secretErr, ErrAlreadyExists) {
		m.log.Info("User exists on ONTAP and K8s secret is present", "username", opts.username, "svm", opts.svmName)
		return passwordFromSecret, nil
	}

	// This block is only reached if userExistsOnOntap was determined to be false earlier.
	password, err := generateSecurePassword()
	if err != nil {
		return "", err
	}

	var (
		application = "http"
		authMethod  = "password"
		pwdVal      = strfmt.Password(password)
		vsadminRole = "vsadmin"
	)

	createAccountParams := security.NewAccountCreateParamsWithContext(ctx)
	createAccountParams.SetInfo(&models.Account{
		Name:     pointer.Pointer(opts.username),
		Password: &pwdVal,
		Role: &models.AccountInlineRole{
			Name: pointer.Pointer(vsadminRole),
		},
		Locked: pointer.Pointer(false),
		Owner: &models.AccountInlineOwner{
			UUID: pointer.Pointer(opts.svmUUID),
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

	if _, createErr := m.ontapClient.Security.AccountCreate(createAccountParams, nil); createErr != nil {
		return "", fmt.Errorf("failed to create ONTAP user '%s' for SVM '%s': %w", opts.username, opts.svmName, createErr)
	}

	m.log.Info("ONTAP user created successfully", "username", opts.username, "svm", opts.svmName, "role", vsadminRole)
	return password, ErrSeedSecretMissing
}

func (m *SvmManager) checkIfAccountExistsForSvm(ctx context.Context, svmName string, kubeSeedSecret string) (string, error) {
	// Check if secret exists in the kube-system namespace in seed
	secretName := fmt.Sprintf(SecretNameFormat, svmName)
	existingSecret := &corev1.Secret{}
	err := m.seedClient.Get(ctx, client.ObjectKey{Namespace: kubeSeedSecret, Name: secretName}, existingSecret)
	if err != nil {
		// If secret is missing in seed
		if apierrors.IsNotFound(err) {
			m.log.Info("Secret not found in seed", "secretName", secretName, "namespace", kubeSeedSecret)
			return "", ErrSeedSecretMissing
		}
		m.log.Error(err, "Failed to get secret from seed", "secretName", secretName, "namespace", kubeSeedSecret)
		return "", fmt.Errorf("failed to get secret %s from namespace %s: %w", secretName, kubeSeedSecret, err)
	}
	// Secret exists, check if password field is present and not empty
	if password, ok := existingSecret.Data["password"]; ok && len(password) > 0 {
		m.log.Info("Secret exists and contains a password", "secretName", secretName, "namespace", kubeSeedSecret)
		return string(password), ErrAlreadyExists
	}
	m.log.Info("Secret exists but password field is missing or empty, considering it missing", "secretName", secretName, "namespace", kubeSeedSecret)
	return "", ErrSeedSecretMissing
}

func generateSecurePassword() (string, error) {
	// Generate a password that is 8 characters long with 2 digits, 0 symbols,
	// allowing upper and lower case letters, disallowing repeat characters.
	res, err := password.Generate(8, 2, 0, false, false)
	if err != nil {
		return "", fmt.Errorf("unable to create a random password:%w", err)
	}
	return res, nil
}

// buildSecret creates a secret with the SVM credentials in the specified namespace
func buildSecret(secretName, userName, password, projectId string) *corev1.Secret {
	// Build and return a Kubernetes secret
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: svmSeedSecretNamespace,
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

func (m *SvmManager) CreateMissingSeedSecret(ctx context.Context, svmName string, ontapclient *ontapv1.Ontap) error {
	// The ontap api doesn't have a way of getting password for users, therefore we update the password and create the seed secret with the updated password
	password, err := generateSecurePassword()
	if err != nil {
		return err
	}

	secparams := security.NewAccountPasswordCreateParamsWithContext(ctx)
	secparams.Info = &models.AccountPassword{
		Name:     pointer.Pointer(defaultSVMUsername),
		Password: pointer.Pointer(strfmt.Password(password)),
		Owner: &models.AccountPasswordInlineOwner{
			Name: &svmName,
		},
	}
	_, err = ontapclient.Security.AccountPasswordCreate(secparams, nil)
	if err != nil {
		var apiErr *runtime.APIError
		if errors.As(err, &apiErr) {
			if !(apiErr.Code == 200 && strings.Contains(apiErr.Error(), "unexpected success response")) {
				return fmt.Errorf("unable to create password for project %s on ontap:%w", svmName, err)
			}
		}
	}

	secretName := fmt.Sprintf(SecretNameFormat, svmName)
	err = m.buildAndCreateSecretInSeed(ctx, secretName, defaultSVMUsername, password, svmName)
	if err != nil {
		return fmt.Errorf("unable to create secret %s in seed %w", secretName, err)
	}

	return nil
}

func (m *SvmManager) buildAndCreateSecretInSeed(ctx context.Context, secretName, userName, password, projectId string) error {
	tridentSecret := buildSecret(secretName, defaultSVMUsername, password, projectId)
	// make this use of managedResource aswell, otherwise seed secret can be deleted
	if err := m.seedClient.Create(ctx, tridentSecret); err != nil {
		return fmt.Errorf("creating secret in seed failed: %w", err)
	}

	return nil
}
