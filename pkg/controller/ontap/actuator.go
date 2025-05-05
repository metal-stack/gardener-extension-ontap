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
	"github.com/go-openapi/strfmt"

	"github.com/gardener/gardener/pkg/apis/core/install"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	"github.com/metal-stack/gardener-extension-ontap/pkg/common"
	"github.com/metal-stack/gardener-extension-ontap/pkg/services"
	"github.com/metal-stack/metal-lib/pkg/tag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/s_vm"
	ontapclient "github.com/metal-stack/ontap-go/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

// FIXME here the logic to deploy the trident operator

const (
	//Why hardcod
	tridentCRDsName      string = "trident-crds"
	tridentRbacMR        string = "trident-rbac"
	tridentBackendsMR    string = "trident-backends"
	tridentLifServicesMR string = "trident-lif-services" // New MR name for LIF services/endpoints
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

	params := s_vm.NewSvmCollectionGetParams()
	result, err := ontap.SVM.SvmCollectionGet(params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ONTAP API and list SVMs: %w", err)
	}

	numSVMs := 0
	if result != nil && result.Payload != nil && result.Payload.NumRecords != nil {
		numSVMs = int(*result.Payload.NumRecords)
	}
	log.Info("Successfully connected to ONTAP. Found %w existing SVMs\n", "svms", numSVMs)

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
	}

	a.log.Info("Shoot annotations", "annotations", shoot.Annotations)
	var projectTag tag.TagMap = shoot.Annotations
	projectId, ok := projectTag.Value(tag.ClusterProject)
	a.log.Info("Found project ID in shoot annotations", "projectId", projectId)
	if !ok || projectId == "" {
		return fmt.Errorf("no project ID found in shoot annotations")
	}
	// Project id "-" to be replaced, ontap doesn't like "-"
	projectId = strings.ReplaceAll(projectId, "-", "")

	a.log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", a.shootNamespace, "managementLifIp", ontapConfig.SvmIpaddresses.ManagementLif, "dataLifIp", ontapConfig.SvmIpaddresses.DataLif)
	err := a.ensureSvmForProject(ctx, a.ontap, ontapConfig.SvmIpaddresses, projectId, a.shootNamespace)
	if err != nil {
		return err
	}

	secretName := fmt.Sprintf(services.SecretNameFormat, projectId)
	a.log.Info("Using credentials from secret in shoot cluster",
		"secretName", secretName,
		"namespace", "kube-system")

	// Define base paths correctly based on the actual structure
	chartPath := services.DefaultChartPath                   // "charts/trident"
	resourcesPath := filepath.Join(chartPath, "resources")   // "charts/trident/resources"
	rbacPath := filepath.Join(resourcesPath, "rbac")         // "charts/trident/resources/rbac"
	crdPath := filepath.Join(resourcesPath, "crds")          // "charts/trident/resources/crds"
	backendPath := filepath.Join(resourcesPath, "backends")  // "charts/trident/resources/backends"
	servicesPath := filepath.Join(resourcesPath, "services") // "charts/trident/resources/services"

	// deploy the secret in the shoot
	err, password := services.GenerateSecurePassword()
	if err != nil {
		// Handle error generating password (e.g., log and return)
		return fmt.Errorf("failed to generate password for svmAdmin: %w", err)
	}
	err = services.DeployTridentSecretsInShootAsMR(ctx, log, projectId, a.shootNamespace, a.client, secretName, services.DefaultSVMUsername, strfmt.Password(password))
	if err != nil {
		return fmt.Errorf("failed to deploy trident secrets as MR: %w", err)
	}

	// 1. Load and Deploy CRDs
	a.log.Info("Loading Trident CRDs", "path", crdPath)
	crdYamls, err := services.LoadYAMLFiles(crdPath) // Load only from the correct crdPath
	if err != nil {
		return fmt.Errorf("failed to load Trident CRDs from %s: %w", crdPath, err)
	}
	if len(crdYamls) > 0 {
		a.log.Info("Deploying Trident CRDs managed resource", "namespace", a.shootNamespace, "name", tridentCRDsName)
		if err := services.DeployResources(ctx, a.client, a.shootNamespace, tridentCRDsName, crdYamls); err != nil {
			return fmt.Errorf("failed to deploy Trident CRDs: %w", err)
		}
		// Wait for CRD Managed Resource to be Ready
		a.log.Info("Waiting for Trident CRDs managed resource to be ready", "name", tridentCRDsName)
		if err := managedresources.WaitUntilHealthyAndNotProgressing(ctx, a.client, a.shootNamespace, tridentCRDsName); err != nil {
			return fmt.Errorf("failed while waiting for Trident CRDs managed resource: %w", err)
		}
		a.log.Info("Trident CRDs managed resource is ready", "name", tridentCRDsName)
	}

	// 2. Load, Template, and Deploy LIF Services/Endpoints
	a.log.Info("Loading LIF Service/Endpoints", "path", servicesPath)
	serviceYamls, err := services.LoadYAMLFiles(servicesPath)
	if err != nil {
		return fmt.Errorf("failed to load LIF Service/Endpoints from %s: %w", servicesPath, err)
	}
	if len(serviceYamls) > 0 {
		replacements := map[string]string{
			"${PROJECT_ID}":        projectId,
			"${MANAGEMENT_LIF_IP}": ontapConfig.SvmIpaddresses.ManagementLif,
			"${DATA_LIF_IP}":       ontapConfig.SvmIpaddresses.DataLif,
		}
		templatedServiceYamls := make(map[string][]byte, len(serviceYamls))
		for name, content := range serviceYamls {
			templatedContent := string(content)
			for placeholder, value := range replacements {
				templatedContent = strings.ReplaceAll(templatedContent, placeholder, value)
			}
			templatedServiceYamls[name] = []byte(templatedContent)
			a.log.Info("Templated LIF Service/Endpoint", "fileName", name, "content", templatedContent) // Verbose logging
		}
		a.log.Info("Deploying LIF Services/Endpoints managed resource", "namespace", a.shootNamespace, "name", tridentLifServicesMR)
		if err := services.DeployResources(ctx, a.client, a.shootNamespace, tridentLifServicesMR, templatedServiceYamls); err != nil {
			return fmt.Errorf("failed to deploy LIF Services/Endpoints: %w", err)
		}
		a.log.Info("Waiting for LIF Services/Endpoints managed resource to be ready", "name", tridentLifServicesMR)
		if err := managedresources.WaitUntilHealthyAndNotProgressing(ctx, a.client, a.shootNamespace, tridentLifServicesMR); err != nil {
			return fmt.Errorf("failed while waiting for LIF Services/Endpoints managed resource: %w", err)
		}
		a.log.Info("LIF Services/Endpoints managed resource is ready", "name", tridentLifServicesMR)
	}

	// 3. Load and Deploy RBAC Resources
	a.log.Info("Loading Trident RBAC resources", "path", rbacPath)
	// Load only from the correct rbacPath, no exclusions needed as CRDs/Backends are separate dirs
	rbacYamls, err := services.LoadYAMLFiles(rbacPath)
	if err != nil {
		return fmt.Errorf("failed to load Trident RBAC resources from %s: %w", rbacPath, err)
	}
	if len(rbacYamls) > 0 {
		a.log.Info("Deploying Trident RBAC managed resource", "namespace", ex.Namespace, "name", tridentRbacMR)
		err = services.DeployResources(ctx, a.client, ex.Namespace, tridentRbacMR, rbacYamls)
		if err != nil {
			return fmt.Errorf("failed to create managed resources for Trident RBAC: %w", err)
		}
		a.log.Info("Trident RBAC managed resource deployment initiated", "name", tridentRbacMR)
	}
	// 4. Process backend templates (only needs ProjectID now)
	a.log.Info("Processing backend templates", "path", backendPath)
	if err := services.ProcessBackendTemplates(a.log, chartPath, projectId, secretName); err != nil {
		return fmt.Errorf("failed to process backend templates: %w", err)
	}
	// 5. Load and Deploy Backend Resources
	a.log.Info("Loading Trident Backend resources", "path", backendPath)
	backendYamls, err := services.LoadYAMLFiles(backendPath) // Load only from the correct backendPath
	if err != nil {
		return fmt.Errorf("failed to load Trident Backend resources from %s: %w", backendPath, err)
	}
	if len(backendYamls) > 0 {
		a.log.Info("Deploying Trident Backends managed resource", "namespace", ex.Namespace, "name", tridentBackendsMR)
		err = services.DeployResources(ctx, a.client, ex.Namespace, tridentBackendsMR, backendYamls)
		if err != nil {
			return fmt.Errorf("failed to create managed resources for Trident Backends: %w", err)
		}
		a.log.Info("Trident Backends managed resource deployment initiated", "name", tridentBackendsMR)
		// Consider waiting for Backend MR if needed.
	}

	a.log.Info("ONTAP extension reconciliation completed successfully")
	return nil
}

// Delete the Extension resource.
func (a *actuator) Delete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	// if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentBackendsMR, false); err != nil {
	// 	return err
	// }
	// if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentBackendsMR); err != nil {
	// 	return err
	// }
	// if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentRbacMR, false); err != nil {
	// 	return err
	// }
	// if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentRbacMR); err != nil {
	// 	return err
	// }
	// if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentCRDsName, false); err != nil {
	// 	return err
	// }
	// if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentCRDsName); err != nil {
	// 	return err
	// }
	// log.Info("ManagedResource for Trident operator successfully deleted.")
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
func (a *actuator) ensureSvmForProject(ctx context.Context, ontapClient *ontapv1.Ontap, SvmIpaddresses common.SvmIpaddresses, projectId string, shootNamespace string) error {

	_, err := services.GetSVMByName(a.log, ontapClient, projectId)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			a.log.Info("No SVM found with project ID, creating new SVM", "projectId", projectId)
			err := services.CreateSVM(ctx, a.log, ontapClient, projectId, a.shootNamespace, a.client, SvmIpaddresses)
			if err != nil {
				return fmt.Errorf("failed to create SVM or network interfaces: %w", err)
			}

			return nil
		}
		return fmt.Errorf("error getting SVM by name: %w", err)
	}

	return nil
}
