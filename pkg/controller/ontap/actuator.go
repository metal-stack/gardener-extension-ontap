package ontap

import (
	"context"
	"errors"
	"fmt"

	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/utils/managedresources"

	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	"github.com/metal-stack/gardener-extension-ontap/pkg/services"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	ontapclient "github.com/metal-stack/ontap-go/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

// FIXME here the logic to deploy the trident operator

const (
	shootNamespace string = "shoot--local--local"
)

// NewActuator returns an actuator responsible for Extension resources.
func NewActuator(log logr.Logger, ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (extension.Actuator, error) {

	ontap, err := createAdminClient(ctx, mgr, config)
	if err != nil {
		return nil, err
	}
	return &actuator{
		log:     log,
		ontap:   ontap,
		client:  mgr.GetClient(),
		decoder: serializer.NewCodecFactory(mgr.GetScheme(), serializer.EnableStrict).UniversalDecoder(),
		config:  config,
	}, nil
}

func createAdminClient(ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (*ontapv1.Ontap, error) {
	client := mgr.GetAPIReader()
	if client == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}

	if config.AdminAuthSecretRef == "" || config.ClusterManagementIp == "" || config.AuthSecretNamespace == "" {
		return nil, fmt.Errorf("missing fields in config")
	}

	var secret corev1.Secret
	err := client.Get(ctx, types.NamespacedName{Name: config.AdminAuthSecretRef, Namespace: config.AuthSecretNamespace}, &secret)
	if err != nil {
		return nil, err
	}
	username, ok := secret.Data["username"]
	if !ok {
		return nil, fmt.Errorf("unable to fetch username from authsecret")
	}
	password, ok := secret.Data["password"]
	if !ok {
		return nil, fmt.Errorf("unable to fetch password from authsecret")
	}
	clusterIp, ok := secret.Data["clusterIp"]
	if !ok {
		return nil, fmt.Errorf("unable to fetch clusterIp from authsecret")
	}

	cfg := ontapclient.Config{
		AdminUser:     string(username),
		AdminPassword: string(password),
		Host:          string(clusterIp),
		InsecureTLS:   true,
	}

	ontap, err := ontapclient.NewAPIClient(cfg)
	if err != nil {
		return nil, err
	}

	return ontap, nil
}

type actuator struct {
	log            logr.Logger
	ontap          *ontapv1.Ontap
	client         client.Client
	shootNamespace string
	decoder        runtime.Decoder
	config         config.ControllerConfiguration
}

// Reconcile fetches the shoot kubeconfig, deploys the helm chart if not installed,
// then creates the necessary secret, storageclass, and TBC in the Shoot.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {

	a.log.Info("Extension in reconcile", "exnnotation", ex.ObjectMeta)
	a.log.Info("annotations", "an", ex.Annotations)
	a.shootNamespace = ex.Namespace
	ontapConfig := &v1alpha1.TridentConfig{}
	if ex.Spec.ProviderConfig != nil {
		_, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, ontapConfig)
		if err != nil {
			return fmt.Errorf("failed to decode provider config: %w", err)
		}
	}

	// Doesn't get the annoation yet. ex.Annotation is "empty"
	// var projectTag tag.TagMap = ex.Annotations
	// projectId, ok := projectTag.Value(tag.ClusterProject)
	// if !ok {
	// 	return fmt.Errorf("shoot doesn't have projectId annotation")
	// }

	err := a.ensureSvmForProject(ctx, a.ontap, "projectId", shootNamespace)
	if err != nil {
		return err
	}

	err = a.deployTridentResources(ctx, ex)
	if err != nil {
		return fmt.Errorf("deploying trident resources failed: %w", err)
	}

	return nil
}

// Delete the Extension resource.
func (a *actuator) Delete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {

	if err := managedresources.Delete(ctx, a.client, ex.Namespace, "trident-operator", false); err != nil {
		return err
	}
	if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, "trident-operator"); err != nil {
		return err
	}
	log.Info("ManagedResource for Trident operator successfully deleted.")
	return nil
}

// ForceDelete the Extension resource
func (a *actuator) ForceDelete(_ context.Context, _ logr.Logger, _ *extensionsv1alpha1.Extension) error {
	return nil
}

// Restore the Extension resource.
func (a *actuator) Restore(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return a.Reconcile(ctx, log, ex)
}

// Migrate the Extension resource.
func (a *actuator) Migrate(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return nil
}

// isSvmAlreadyInstalled checks,
// FIXME check for race condition after creating svm to create user for him
func (a *actuator) ensureSvmForProject(ctx context.Context, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string) error {

	_, err := services.GetSVMByName(a.log, ontapClient, projectId)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			a.log.Info("no svm found with projectId, creating svm...")
			err = services.CreateSVM(a.log, ontapClient, projectId)
			if err != nil {
				return err
			}
			err = services.CreateUserAndSecret(ctx, a.log, ontapClient, projectId, a.shootNamespace, a.client)
			if err != nil {
				return err
			}
			return nil
		}
		return err
	}
	a.log.Info("svm created, creating user and admin svm scoped")
	err = services.CreateUserAndSecret(ctx, a.log, ontapClient, projectId, shootNamespace, a.client)
	if err != nil {
		return err
	}
	return nil
}

func (a *actuator) deployTridentResources(ctx context.Context, ex *extensionsv1alpha1.Extension) error {
	yamlDir := "/charts/trident"

	if err := services.DeployYAMLsToShoot(
		ctx,
		a.client,
		ex.Namespace,
		"trident-operator",
		yamlDir,
	); err != nil {
		return fmt.Errorf("deploying trident from yamls failed: %w", err)
	}

	a.log.Info("trident operator mr ready to be deployed into shoot")
	return nil
}
