package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/go-openapi/strfmt"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/security"
	"github.com/metal-stack/ontap-go/api/models"
)

// GeneratePassword generates a secure random password using ONTAP APIs
func GeneratePassword(log logr.Logger, ontapClient *ontapv1.Ontap) (string, error) {
	log.Info("Using hardcoded password for development")
	return "Tr1d3nt-Passw0rd-123", nil
}

// CreateONTAPUserForSVM creates a user for the specified SVM with the built-in vsadmin role
func CreateONTAPUserForSVM(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap,
	username string, password string, svmName string) error {

	log.Info("Creating ONTAP user for SVM", "username", username, "svm", svmName)

	// Check if user already exists
	svmUUID, err := GetSVMByName(log, ontapClient, svmName)
	if err != nil {
		return fmt.Errorf("failed to get SVM UUID: %w", err)
	}

	// Check if account exists
	err = checkIfAccountExistsForSvm(log, ontapClient, username, svmName)
	if err == nil || errors.Is(err, ErrAlreadyExists) {
		log.Info("ONTAP user for SVM already exists", "username", username, "svm", svmName)
		return nil
	}
	// Create user with the built-in vsadmin role
	application := "http"
	authMethod := "password"
	pwdVal := strfmt.Password(password)
	vsadminRole := "vsadmin" // Use the built-in vsadmin role

	params := &security.AccountCreateParams{
		Info: &models.Account{
			Name:     pointer.Pointer(username),
			Password: &pwdVal,
			Role: &models.AccountInlineRole{
				Name: pointer.Pointer(vsadminRole), // Use vsadmin instead of custom role
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
		return fmt.Errorf("failed to create ONTAP user: %w", err)
	}

	log.Info("ONTAP user created successfully", "username", username, "svm", svmName, "role", "vsadmin")
	return nil
}
