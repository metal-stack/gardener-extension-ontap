package ontap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/utils/managedresources"

	"github.com/gardener/gardener/pkg/apis/core/install"
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

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/metal-stack/metal-lib/pkg/tag"
	corev1 "k8s.io/api/core/v1"
)

// FIXME here the logic to deploy the trident operator

const (
	shootNamespace    string = "shoot--local--local"
	tridentCRDsName   string = "trident-crds"
	tridentBackendsMR string = "trident-backends" // New MR for backends
	tridentOperatorMR string = "trident-operator" // MR for core components
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

	// params := s_vm.NewSvmCollectionGetParams()
	// result, err := ontap.SVM.SvmCollectionGet(params, nil)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to connect to ONTAP API and list SVMs: %w", err)
	// }

	// numSVMs := 0
	// if result != nil && result.Payload != nil && result.Payload.NumRecords != nil {
	// 	numSVMs = int(*result.Payload.NumRecords)
	// }
	// log.Info("Successfully connected to ONTAP. Found %d existing SVMs\n", numSVMs)

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

// Reconcile handles extension creation and updates.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	a.shootNamespace = ex.Namespace
	ontapConfig := &v1alpha1.TridentConfig{}
	if ex.Spec.ProviderConfig != nil {
		_, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, ontapConfig)
		if err != nil {
			return fmt.Errorf("failed to decode provider config: %w", err)
		}
	}

	cluster := &extensionsv1alpha1.Cluster{}
	if err := a.client.Get(ctx, client.ObjectKey{Name: ex.Namespace}, cluster); err != nil {
		return fmt.Errorf("failed to get cluster object: %w", err)
	}

	shoot := &gardencorev1beta1.Shoot{}
	if cluster.Spec.Shoot.Raw != nil {
		if _, _, err := a.decoder.Decode(cluster.Spec.Shoot.Raw, nil, shoot); err != nil {
			log.Error(err, "failed to decode shoot, continuing with partial shoot object")
		}
	} else {
		return fmt.Errorf("shoot spec in cluster resource is empty")
	}

	a.log.Info("Shoot annotations", "annotations", shoot.Annotations)
	var projectTag tag.TagMap = shoot.Annotations
	projectId, ok := projectTag.Value(tag.ClusterProject)
	if !ok || projectId == "" {
		return fmt.Errorf("no project ID found in shoot annotations")
	}

	projectId = strings.ReplaceAll(projectId, "-", "")
	a.log.Info("Found project ID in shoot annotations", "projectId", projectId)

	a.log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", a.shootNamespace)
	dataLif, managementLif, err := a.ensureSvmForProject(ctx, a.ontap, projectId, a.shootNamespace)
	if err != nil {
		return err
	}

	secretName := fmt.Sprintf(services.CredentialSecretNameFormat, projectId)
	a.log.Info("Using credentials from secret in shoot cluster",
		"secretName", secretName,
		"namespace", "kube-system")

	// Setup paths
	chartPath := services.DefaultChartPath
	baseResourcePath := filepath.Join(chartPath, services.ResourcesDir)
	crdPath := filepath.Join(baseResourcePath, services.CRDsDir)
	// backendsPath := filepath.Join(baseResourcePath, services.BackendsDir) // Path for optional backend MR

	// Process backend templates in place (needed regardless of whether backend MR is deployed)
	if err := services.ProcessBackendTemplates(a.log, chartPath, projectId, secretName, dataLif, managementLif); err != nil {
		return fmt.Errorf("failed to process backend templates: %w", err)
	}

	// --- 1. Deploy CRDs ---
	a.log.Info("Loading Trident CRDs", "path", crdPath)
	crdYamls, err := services.LoadYAMLFiles(crdPath)
	if err != nil {
		return fmt.Errorf("failed to load Trident CRDs: %w", err)
	}
	a.log.Info("Deploying Trident CRDs managed resource", "namespace", a.shootNamespace, "name", tridentCRDsName)
	if err := services.DeployResources(ctx, a.client, a.shootNamespace, tridentCRDsName, crdYamls); err != nil {
		return fmt.Errorf("failed to deploy Trident CRDs MR: %w", err)
	}

	// --- 2. Wait for CRDs to be Ready ---
	a.log.Info("Waiting for Trident CRDs managed resource to be ready", "name", tridentCRDsName)
	if err := managedresources.WaitUntilHealthy(ctx, a.client, a.shootNamespace, tridentCRDsName); err != nil {
		return fmt.Errorf("failed while waiting for Trident CRDs managed resource: %w", err)
	}
	a.log.Info("Trident CRDs managed resource is ready", "name", tridentCRDsName)

	// --- 3. (Optional/Commented Out) Deploy Backends ---
	/*
		a.log.Info("Loading Trident Backends", "path", backendsPath)
		backendYamls, err := services.LoadYAMLFiles(backendsPath)
		if err != nil {
			return fmt.Errorf("failed to load Trident Backends: %w", err)
		}
			a.log.Info("Deploying Trident Backends managed resource", "namespace", ex.Namespace, "name", tridentBackendsMR)
		if err := services.DeployResources(ctx, a.client, ex.Namespace, tridentBackendsMR, backendYamls); err != nil {
			return fmt.Errorf("failed to deploy Trident Backends MR: %w", err)
		}
		a.log.Info("Trident Backends managed resource deployment initiated", "name", tridentBackendsMR)
	*/

	// --- 4. Deploy Core Operator Resources ---
	a.log.Info("Loading core Trident resources", "path", baseResourcePath, "excluding", []string{services.CRDsDir, services.BackendsDir})
	// Load everything from resources/, excluding crds/ and backends/
	coreOperatorYamls, err := services.LoadYAMLFiles(baseResourcePath, services.CRDsDir, services.BackendsDir)
	if err != nil {
		return fmt.Errorf("failed to load core Trident resources: %w", err)
	}
	a.log.Info("Deploying Trident Operator managed resource", "namespace", ex.Namespace, "name", tridentOperatorMR)
	err = services.DeployResources(ctx, a.client, ex.Namespace, tridentOperatorMR, coreOperatorYamls)
	if err != nil {
		return fmt.Errorf("failed to deploy Trident Operator MR: %w", err)
	}
	a.log.Info("Trident Operator managed resource deployment initiated", "name", tridentOperatorMR)

	a.log.Info("ONTAP extension reconciliation completed successfully")
	return nil
}

// Delete the Extension resource.
func (a *actuator) Delete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	// Delete in reverse order of creation (approximately)

	// Delete Core Operator MR
	if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentOperatorMR, false); err != nil {
		return fmt.Errorf("failed to delete managed resource %s: %w", tridentOperatorMR, err)
	}
	if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentOperatorMR); err != nil {
		return fmt.Errorf("failed waiting for deletion of managed resource %s: %w", tridentOperatorMR, err)
	}
	log.Info("ManagedResource deleted successfully", "name", tridentOperatorMR)

	// (Optional/Commented Out) Delete Backends MR
	/*
		if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentBackendsMR, false); err != nil {
			return fmt.Errorf("failed to delete managed resource %s: %w", tridentBackendsMR, err)
		}
		if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentBackendsMR); err != nil {
			return fmt.Errorf("failed waiting for deletion of managed resource %s: %w", tridentBackendsMR, err)
		}
		log.Info("ManagedResource deleted successfully", "name", tridentBackendsMR)
	*/

	// Delete CRDs MR
	if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentCRDsName, false); err != nil {
		return fmt.Errorf("failed to delete managed resource %s: %w", tridentCRDsName, err)
	}
	if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentCRDsName); err != nil {
		return fmt.Errorf("failed waiting for deletion of managed resource %s: %w", tridentCRDsName, err)
	}
	log.Info("ManagedResource deleted successfully", "name", tridentCRDsName)

	log.Info("All Trident managed resources successfully deleted.")
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

// ensureSvmForProject creates an SVM for the project if it doesn't exist yet
func (a *actuator) ensureSvmForProject(ctx context.Context, ontapClient *ontapv1.Ontap, projectId string, shootNamespace string) (string, string, error) {
	uuid, err := services.GetSVMByName(a.log, ontapClient, projectId)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			a.log.Info("No SVM found with project ID, creating new SVM", "projectId", projectId)

			dataLif, managementLif, err := services.CreateSVM(a.log, ontapClient, projectId)
			if err != nil {
				return "", "", fmt.Errorf("failed to create SVM or network interfaces: %w", err)
			}

			a.log.Info("SVM creation completed", "projectId", projectId, "dataLif", dataLif, "managementLif", managementLif)

			a.log.Info("Creating user and secret with network information",
				"projectId", projectId,
				"dataLif", dataLif,
				"managementLif", managementLif)

			if err = services.CreateUserAndSecret(ctx, a.log, ontapClient, projectId, shootNamespace, a.client, dataLif, managementLif); err != nil {
				return "", "", fmt.Errorf("failed to create user and secret: %w", err)
			}

			return dataLif, managementLif, nil
		}
		return "", "", fmt.Errorf("error getting SVM by name: %w", err)
	}

	a.log.Info("SVM already exists", "projectId", projectId, "uuid", uuid)

	dataLif, managementLif, err := services.GetSVMNetworkInterfaces(a.log, ontapClient, uuid)
	if err != nil {
		return "", "", fmt.Errorf("failed to get network interfaces for existing SVM: %w", err)
	}

	a.log.Info("Retrieved network interfaces for existing SVM",
		"projectId", projectId,
		"dataLif", dataLif,
		"managementLif", managementLif)

	if err = services.CreateUserAndSecret(ctx, a.log, ontapClient, projectId, shootNamespace, a.client, dataLif, managementLif); err != nil {
		return "", "", fmt.Errorf("failed to create user and secret: %w", err)
	}

	return dataLif, managementLif, nil
}
