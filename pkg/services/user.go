package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-openapi/strfmt"
	"github.com/metal-stack/ontap-go/api/client/security"
	"github.com/metal-stack/ontap-go/api/models"
)

// CreateUser creates an svm scoped account set to vsadmin role
func CreateUserAndSecret(ctx context.Context, log logr.Logger, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string, seedClient client.Client) error {
	log.Info("creating user for svm", "svm", projectId)

	application := "http"
	authentication_methods := "password"
	accountName := "FIXM2E"
	vsadmin := "vsadmin"
	var password strfmt.Password = "fsqe2020"
	secretName := "my-secret"
	params := &security.AccountCreateParams{
		Info: &models.Account{

			Owner: &models.AccountInlineOwner{
				Name: &projectId,
			},
			// role - RBAC role for the user account. Defaulted to admin for cluster user account and to vsadmin for SVM-scoped account.
			// can be adjusted to have less privileges in the svm, for now it's fine
			Role: &models.AccountInlineRole{
				Name: &vsadmin,
			},
			Password: &password,
			AccountInlineApplications: []*models.AccountApplication{
				{
					Application: &application,
					AuthenticationMethods: []*string{
						&authentication_methods,
					}},
			},
			Name: &accountName,
		},
	}

	err := checkIfAccountExistsForSvm(log, ontapClient, accountName, projectId)
	if err != nil {
		// user already exists no need to return err
		if errors.Is(err, ErrAlreadyExists) {
			err = deployTridentSecrets(ctx, log, secretName, accountName, password, projectId, shootNamespace, seedClient)
			if err != nil {
				return err
			}
			return nil
		}
		return err
	}
	log.Info("account for svm doesn't exist, creating...")
	_, err = ontapClient.Security.AccountCreate(params, nil)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	err = deployTridentSecrets(ctx, log, secretName, accountName, password, projectId, shootNamespace, seedClient)
	if err != nil {
		return err
	}
	log.Info("user created successfully.")
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

func deployTridentSecrets(ctx context.Context, log logr.Logger, secretName string, userName string, password strfmt.Password, projectId string, shootNamespace string, seedClient client.Client) error {

	objs := BuildTridentSecret(secretName, userName, password.String(), projectId)
	clientObjs := []client.Object{objs}
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
		"trident-operator",
		"trident",
		false,
		shootResources,
	)
	if err != nil {
		return fmt.Errorf("failed to create ManagedResource for trident operator: %w", err)
	}

	log.Info("trident MR operator created in the seed, will be applied to the shoot...")
	return nil
}
