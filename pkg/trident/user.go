package trident

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sethvargo/go-password/password"

	"github.com/metal-stack/metal-lib/pkg/pointer"
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

// validateAndEnsureCompleteUserState validates all user components and creates missing parts
func (m *SvmManager) validateAndEnsureCompleteUserState(ctx context.Context, opts userAndSecretOptions) error {
	m.log.Info("Validating complete user state", "svm", opts.projectID, "namespace", opts.svmSeedSecretNamespace)

	// 1. Check K8s secret state first
	secretName := fmt.Sprintf(SecretNameFormat, opts.projectID)
	existingPassword, secretErr := m.checkIfAccountExistsForSvm(ctx, opts.projectID, opts.svmSeedSecretNamespace)

	// 2. Check if ontap user exists already
	ontapUserExists, ontapPassword, userErr := m.validateONTAPUserExists(ctx, opts)

	// 3. Determine what needs to be created/updated
	switch {
	// Both exist - validate password consistency
	case errors.Is(secretErr, ErrAlreadyExists) && ontapUserExists:
		return m.validatePasswordConsistency(ctx, opts, existingPassword, ontapPassword)
	// Secret exists but ONTAP user missing - create ONTAP user with existing password
	case errors.Is(secretErr, ErrAlreadyExists) && !ontapUserExists:
		m.log.Info("K8s secret exists but ONTAP user missing, creating ONTAP user", "svm", opts.projectID)
		return m.createONTAPUserWithPassword(ctx, opts, existingPassword)

	case errors.Is(secretErr, ErrSeedSecretMissing) && ontapUserExists:
		// ONTAP user exists but secret missing - create secret with new password
		m.log.Info("ONTAP user exists but K8s secret missing, updating password and creating secret", "svm", opts.projectID)
		newPassword, err := m.resetONTAPUserPassword(ctx, opts)
		if err != nil {
			return err
		}
		return m.buildAndCreateSecretInSeed(ctx, secretName, defaultSVMUsername, newPassword, opts.projectID)

	case errors.Is(secretErr, ErrSeedSecretMissing) && !ontapUserExists:
		// Neither exists - create both
		m.log.Info("Neither ONTAP user nor K8s secret exist, creating both", "svm", opts.projectID)
		return m.createCompleteUserAndSecret(ctx, opts)

	default:
		// Handle other errors
		if secretErr != nil && !errors.Is(secretErr, ErrSeedSecretMissing) && !errors.Is(secretErr, ErrAlreadyExists) {
			return fmt.Errorf("failed to check K8s secret state: %w", secretErr)
		}
		if userErr != nil {
			return fmt.Errorf("failed to check ONTAP user state: %w", userErr)
		}
		return fmt.Errorf("unexpected state combination")
	}
}

// validateONTAPUserExists checks if ONTAP user exists by attempting creation
func (m *SvmManager) validateONTAPUserExists(ctx context.Context, opts userAndSecretOptions) (bool, string, error) {
	// Generate a test password
	testPassword, err := generateSecurePassword()
	if err != nil {
		return false, "", err
	}

	ontapOpts := ontapUserOptions{
		username:         defaultSVMUsername,
		svmName:          opts.projectID,
		kubeSeedSecretNs: opts.svmSeedSecretNamespace,
		svmUUID:          opts.svmUUID,
	}

	// Try to create user if 409, user exists
	_, createErr := m.attemptUserCreation(ctx, ontapOpts, testPassword)
	if createErr == nil {
		// User was created successfully, so it didn't exist before
		return false, testPassword, nil
	}

	// Check if it's a 409 conflict
	var apiErr *security.AccountCreateDefault
	if errors.As(createErr, &apiErr) && apiErr.Code() == 409 {
		// User exists - return true with empty password (we don't know the current password)
		return true, "", nil
	}

	return false, "", fmt.Errorf("failed to validate ONTAP user existence: %w", createErr)
}

// attemptUserCreation tries to create a user with given password
func (m *SvmManager) attemptUserCreation(ctx context.Context, opts ontapUserOptions, password string) (string, error) {
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

	_, err := m.ontapClient.Security.AccountCreate(createAccountParams, nil)
	return password, err
}

// createONTAPUserWithPassword creates ONTAP user with specific password
func (m *SvmManager) createONTAPUserWithPassword(ctx context.Context, opts userAndSecretOptions, password string) error {
	ontapOpts := ontapUserOptions{
		username:         defaultSVMUsername,
		svmName:          opts.projectID,
		kubeSeedSecretNs: opts.svmSeedSecretNamespace,
		svmUUID:          opts.svmUUID,
	}

	_, err := m.attemptUserCreation(ctx, ontapOpts, password)
	if err != nil {
		return fmt.Errorf("failed to create ONTAP user with existing password: %w", err)
	}

	m.log.Info("Successfully created ONTAP user with existing password", "svm", opts.projectID)
	return nil
}

// resetONTAPUserPassword updates existing user password
func (m *SvmManager) resetONTAPUserPassword(ctx context.Context, opts userAndSecretOptions) (string, error) {
	password, err := generateSecurePassword()
	if err != nil {
		return "", err
	}

	_, err = m.updateExistingUserPassword(ctx, defaultSVMUsername, opts.projectID, password)
	if err != nil {
		return "", fmt.Errorf("failed to reset ONTAP user password: %w", err)
	}

	return password, nil
}

// createCompleteUserAndSecret creates both ONTAP user and K8s secret
func (m *SvmManager) createCompleteUserAndSecret(ctx context.Context, opts userAndSecretOptions) error {
	password, err := generateSecurePassword()
	if err != nil {
		return err
	}

	// Create ONTAP user
	ontapOpts := ontapUserOptions{
		username:         defaultSVMUsername,
		svmName:          opts.projectID,
		kubeSeedSecretNs: opts.svmSeedSecretNamespace,
		svmUUID:          opts.svmUUID,
	}

	_, err = m.attemptUserCreation(ctx, ontapOpts, password)
	if err != nil {
		return fmt.Errorf("failed to create ONTAP user: %w", err)
	}

	// Create K8s secret
	secretName := fmt.Sprintf(SecretNameFormat, opts.projectID)
	err = m.buildAndCreateSecretInSeed(ctx, secretName, defaultSVMUsername, password, opts.projectID)
	if err != nil {
		return fmt.Errorf("failed to create K8s secret after ONTAP user creation: %w", err)
	}

	m.log.Info("Successfully created complete user and secret", "svm", opts.projectID)
	return nil
}

// validatePasswordConsistency ensures ONTAP and K8s passwords match
func (m *SvmManager) validatePasswordConsistency(ctx context.Context, opts userAndSecretOptions, secretPassword, ontapPassword string) error {
	m.log.Info("Both ONTAP user and K8s secret exist, validating consistency", "svm", opts.projectID)

	// Since we can't directly validate ONTAP password, we'll try to update it
	// If the update succeeds, we know the user is functional
	_, err := m.updateExistingUserPassword(ctx, defaultSVMUsername, opts.projectID, secretPassword)
	if err != nil {
		// If password update fails, try to fix by generating new password
		m.log.Info("Password consistency validation failed, resetting password", "svm", opts.projectID)
		newPassword, resetErr := m.resetONTAPUserPassword(ctx, opts)
		if resetErr != nil {
			return fmt.Errorf("failed to reset password for consistency: %w", resetErr)
		}

		// Update K8s secret with new password
		secretName := fmt.Sprintf(SecretNameFormat, opts.projectID)
		return m.updateSecretInSeed(ctx, secretName, opts.svmSeedSecretNamespace, defaultSVMUsername, newPassword, opts.projectID)
	}

	m.log.Info("Password consistency validation successful", "svm", opts.projectID)
	return nil
}

// updateSecretInSeed updates an existing secret in the seed cluster
func (m *SvmManager) updateSecretInSeed(ctx context.Context, secretName, namespace, username, password, projectId string) error {
	// Try to get existing secret first
	existingSecret := &corev1.Secret{}
	err := m.seedClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, existingSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Secret doesn't exist, create it
			return m.buildAndCreateSecretInSeed(ctx, secretName, username, password, projectId)
		}
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Secret exists, update it
	existingSecret.Data = map[string][]byte{
		"username": []byte(username),
		"password": []byte(password),
	}
	existingSecret.StringData = nil

	if err := m.seedClient.Update(ctx, existingSecret); err != nil {
		return fmt.Errorf("failed to update secret in seed: %w", err)
	}

	m.log.Info("Successfully updated secret in seed", "secretName", secretName, "namespace", namespace)
	return nil
}

// CreateUserAndSecret creates an svm scoped account set to vsadmin role.
func (m *SvmManager) CreateUserAndSecret(ctx context.Context, opts userAndSecretOptions) error {
	m.log.Info("Ensuring complete user and secret state", "svm", opts.projectID)

	// Use comprehensive validation instead of simple creation
	return m.validateAndEnsureCompleteUserState(ctx, opts)
}

// updateExistingUserPassword updates the password for an existing ONTAP user
func (m *SvmManager) updateExistingUserPassword(ctx context.Context, username, svmName, password string) (string, error) {
	secparams := security.NewAccountPasswordCreateParamsWithContext(ctx)
	secparams.Info = &models.AccountPassword{
		Name:     pointer.Pointer(username),
		Password: pointer.Pointer(strfmt.Password(password)),
		Owner: &models.AccountPasswordInlineOwner{
			Name: &svmName,
		},
	}

	_, pwdErr := m.ontapClient.Security.AccountPasswordCreate(secparams, nil)
	if pwdErr == nil {
		m.log.Info("Password updated successfully", "username", username, "svm", svmName)
		return password, ErrSeedSecretMissing
	}

	// swallow unexpected success error
	if strings.Contains(pwdErr.Error(), "unexpected success response") && strings.Contains(pwdErr.Error(), "status 200") {
		m.log.Info("Password updated successfully (reported as error)", "username", username, "svm", svmName)
		return password, ErrSeedSecretMissing
	}

	return "", fmt.Errorf("failed to update password for existing user '%s' in SVM '%s': %w", username, svmName, pwdErr)
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

func (m *SvmManager) buildAndCreateSecretInSeed(ctx context.Context, secretName, userName, password, projectId string) error {
	tridentSecret := buildSecret(secretName, defaultSVMUsername, password, projectId)
	// make this use of managedResource aswell, otherwise seed secret can be deleted
	if err := m.seedClient.Create(ctx, tridentSecret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Secret already exists, try to update it instead
			m.log.Info("Secret already exists, updating it", "secretName", secretName)
			return m.updateSecretInSeed(ctx, secretName, svmSeedSecretNamespace, userName, password, projectId)
		}
		return fmt.Errorf("creating secret in seed failed: %w", err)
	}

	m.log.Info("Successfully created secret in seed", "secretName", secretName)
	return nil
}
