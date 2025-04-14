package ontap

import (
	"context"
	"errors"
	"fmt"
	"time"

	extensionsconfig "github.com/gardener/gardener/extensions/pkg/apis/config"
	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	gutil "github.com/gardener/gardener/extensions/pkg/util"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/utils/managedresources"

	"github.com/gardener/gardener/pkg/apis/core/install"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	"github.com/metal-stack/gardener-extension-ontap/pkg/services"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
	ontapclient "github.com/metal-stack/ontap-go/pkg/client"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/metal-stack/metal-lib/pkg/tag"
	corev1 "k8s.io/api/core/v1"
)

// FIXME here the logic to deploy the trident operator

const (
	shootNamespace string = "shoot--local--local"
)

// NewActuator returns an actuator responsible for Extension resources.
func NewActuator(log logr.Logger, ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (extension.Actuator, error) {

	scheme := mgr.GetScheme()
	install.Install(scheme)

	ontap, err := createAdminClient(ctx, mgr, config)
	if err != nil {
		return nil, err
	}
	return &actuator{
		log:     log,
		ontap:   ontap,
		client:  mgr.GetClient(),
		decoder: serializer.NewCodecFactory(mgr.GetScheme()).UniversalDeserializer(),
		config:  config,
	}, nil
}

func createAdminClient(ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (*ontapv1.Ontap, error) {
	client := mgr.GetAPIReader()
	if client == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}

	if config.AdminAuthSecretRef == "" || config.ClusterManagementIp == "" || config.AuthSecretNamespace == "" {
		return nil, fmt.Errorf("missing fields in config: AdminAuthSecretRef=%s, ClusterManagementIp=%s, AuthSecretNamespace=%s",
			config.AdminAuthSecretRef, config.ClusterManagementIp, config.AuthSecretNamespace)
	}

	var secret corev1.Secret
	err := client.Get(ctx, types.NamespacedName{Name: config.AdminAuthSecretRef, Namespace: config.AuthSecretNamespace}, &secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth secret: %w", err)
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
		clusterIp = []byte(config.ClusterManagementIp)
		fmt.Printf("Using clusterIp from config: %s\n", clusterIp)
	}

	log.Info("Connecting to ONTAP using: username=%s, host=%s\n", string(username), string(clusterIp))

	cfg := ontapclient.Config{
		AdminUser:     string(username),
		AdminPassword: string(password),
		Host:          string(clusterIp),
		InsecureTLS:   true,
	}

	ontap, err := ontapclient.NewAPIClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONTAP API client: %w", err)
	}

	// Validate connection by trying to list SVMs
	log.Info("Validating ONTAP connection\n")
	params := s_vm.NewSvmCollectionGetParams()
	result, err := ontap.SVM.SvmCollectionGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ONTAP API and list SVMs: %w", err)
	}

	// Log the number of SVMs found to validate connection
	numSVMs := 0
	if result != nil && result.Payload != nil && result.Payload.NumRecords != nil {
		numSVMs = int(*result.Payload.NumRecords)
	}
	log.Info("Successfully connected to ONTAP. Found %d existing SVMs\n", numSVMs)

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
	a.shootNamespace = ex.Namespace
	ontapConfig := &v1alpha1.TridentConfig{}
	if ex.Spec.ProviderConfig != nil {
		_, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, ontapConfig)
		if err != nil {
			return fmt.Errorf("failed to decode provider config: %w", err)
		}
	}

	// Get the Cluster object for this extension
	cluster := &extensionsv1alpha1.Cluster{}
	if err := a.client.Get(ctx, client.ObjectKey{Name: ex.Namespace}, cluster); err != nil {
		return fmt.Errorf("failed to get cluster object: %w", err)
	}

	// Decode the Shoot from the cluster's spec with less strict decoding
	shoot := &gardencorev1beta1.Shoot{}
	if cluster.Spec.Shoot.Raw != nil {
		if _, _, err := a.decoder.Decode(cluster.Spec.Shoot.Raw, nil, shoot); err != nil {
			log.Error(err, "failed to decode shoot, continuing with partial shoot object")
			// Continue anyway since we only need annotations
		}
	} else {
		return fmt.Errorf("shoot spec in cluster resource is empty")
	}

	// Extract project ID from shoot annotations
	a.log.Info("Shoot annotations", "annotations", shoot.Annotations)
	var projectTag tag.TagMap = shoot.Annotations
	projectId, ok := projectTag.Value(tag.ClusterProject)

	if !ok {
		return fmt.Errorf("shoot doesn't have required projectId annotation (cluster.metal-stack.io/project)")
	}

	a.log.Info("Found project ID in shoot annotations", "projectId", projectId)

	// Create client for the Shoot
	_, shootClient, err := gutil.NewClientForShoot(ctx, a.client, a.shootNamespace, client.Options{}, extensionsconfig.RESTOptions{})
	if err != nil {
		return fmt.Errorf("failed to create shoot client: %w", err)
	}

	// Use the shoot client to create namespace if it doesn't exist
	tridentNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "trident",
		},
	}

	// Check if the namespace exists first
	existingNamespace := &corev1.Namespace{}
	err = shootClient.Get(ctx, client.ObjectKey{Name: "trident"}, existingNamespace)
	namespaceExists := err == nil

	if !namespaceExists {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if trident namespace exists: %w", err)
		}

		// Namespace doesn't exist, create it
		err = shootClient.Create(ctx, tridentNamespace)
		if err != nil {
			return fmt.Errorf("failed to create trident namespace in shoot: %w", err)
		}
		a.log.Info("Created trident namespace in shoot")
	} else {
		a.log.Info("Trident namespace already exists in shoot")
	}

	a.log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", a.shootNamespace)
	err = a.ensureSvmForProject(ctx, a.ontap, projectId, a.shootNamespace)
	if err != nil {
		return err
	}

	// Make sure the namespace is created before deploying resources
	if !namespaceExists {
		a.log.Info("Waiting for trident namespace to be ready before deploying resources")
		// Small delay to ensure namespace is fully created
		time.Sleep(2 * time.Second)
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

	uuid, err := services.GetSVMByName(a.log, ontapClient, projectId)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			a.log.Info("no svm found with projectId, creating svm...")
			err = services.CreateSVM(a.log, ontapClient, projectId)
			if err != nil {
				return err
			}

			// Check if the SVM was created successfully
			uuid, err = services.GetSVMByName(a.log, ontapClient, projectId)
			if err != nil {
				if errors.Is(err, services.ErrNotFound) {
					// The SVM is still being created, we'll need to retry later
					a.log.Info("SVM creation initiated but not yet complete, will retry in next reconciliation")
					return nil
				}
				return fmt.Errorf("error checking if SVM was created: %w", err)
			}

			// SVM was created successfully, now create the user and secret
			err = services.CreateUserAndSecret(ctx, a.log, ontapClient, projectId, shootNamespace, a.client)
			if err != nil {
				return err
			}
			return nil
		}
		return err
	}

	a.log.Info("svm already exists, creating user and admin svm scoped", "uuid", uuid)
	err = services.CreateUserAndSecret(ctx, a.log, ontapClient, projectId, shootNamespace, a.client)
	if err != nil {
		return err
	}
	return nil
}

func (a *actuator) deployTridentResources(ctx context.Context, ex *extensionsv1alpha1.Extension) error {
	// Use the relative path to the charts directory
	// This path should be relative to where the extension runs in the container
	yamlDir := "/charts/trident"

	a.log.Info("Deploying Trident resources from directory", "directory", yamlDir)

	if err := services.DeployYAMLsToShoot(
		ctx,
		a.client,
		ex.Namespace,
		"trident-operator",
		yamlDir,
	); err != nil {
		return fmt.Errorf("deploying trident from yamls failed: %w", err)
	}

	a.log.Info("Trident operator managed resource ready to be deployed into shoot")
	return nil
}
