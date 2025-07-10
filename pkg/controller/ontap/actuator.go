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

	firewallv1 "github.com/metal-stack/firewall-controller/v2/api/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	"github.com/metal-stack/gardener-extension-ontap/pkg/services"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	"github.com/metal-stack/metal-lib/pkg/tag"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	tridentCRDsName        string = "trident-crds"
	tridentInitMR          string = "trident-init"
	tridentBackendsMR      string = "trident-backends"
	tridentLifServicesMR   string = "trident-lif-services" // New MR name for LIF services/endpoints
	svmSeedSecretNamespace string = "kube-system"

	defaultChartPath = "charts/trident"
)

type actuator struct {
	log            logr.Logger
	ontap          *ontapv1.Ontap
	client         client.Client
	svnManager     *services.SvnManager
	shootNamespace string
	decoder        runtime.Decoder
	config         config.ControllerConfiguration
}

// NewActuator returns an actuator responsible for Extension resources.
func NewActuator(log logr.Logger, ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (extension.Actuator, error) {
	scheme := mgr.GetScheme()
	install.Install(scheme)

	ontap, err := createAdminClient(ctx, mgr, config)
	if err != nil {
		return nil, err
	}

	client := mgr.GetClient()

	svnManager := services.NewSvnManager(log, ontap, client)
	return &actuator{
		log:        log,
		ontap:      ontap,
		client:     client,
		svnManager: svnManager,
		decoder:    serializer.NewCodecFactory(mgr.GetScheme()).UniversalDeserializer(),
		config:     config,
	}, nil
}

func createAdminClient(ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (*ontapv1.Ontap, error) {
	client := mgr.GetAPIReader()
	if client == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}

	if config.AdminAuthSecretRef == "" || config.AuthSecretNamespace == "" {
		return nil, fmt.Errorf("missing fields in config: AdminAuthSecretRef=%s, AuthSecretNamespace=%s",
			config.AdminAuthSecretRef, config.AuthSecretNamespace)
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
		return nil, fmt.Errorf("unable to fetch clusterip from authsecret")
	}

	log.Info("Connecting to ONTAP", "username", string(username), "host", string(clusterIp))

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
	log.Info("Successfully connected to ONTAP. Found existing SVMs", "svms", numSVMs)

	return ontap, nil
}

// Reconcile handles extension creation and updates.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	a.shootNamespace = ex.Namespace
	ontapConfig := &ontapv1alpha1.TridentConfig{}
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

	a.log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", svmSeedSecretNamespace,
		"managementLifIp", ontapConfig.SvmIpaddresses.ManagementLif, "dataLifIps", ontapConfig.SvmIpaddresses.DataLifs)
	err := a.ensureSvmForProject(ctx, ontapConfig.SvmIpaddresses, projectId, svmSeedSecretNamespace)
	if err != nil {
		return err
	}

	secretName := fmt.Sprintf(services.SecretNameFormat, projectId)
	a.log.Info("Using credentials from secret in shoot cluster", "secretName", secretName, "namespace", "kube-system")

	// Define base paths correctly based on the actual structure
	chartPath := defaultChartPath                                   // "charts/trident"
	resourcesPath := filepath.Join(chartPath, "resources")          // "charts/trident/resources"
	tridentInitPath := filepath.Join(resourcesPath, "trident-init") // "charts/trident/resources/trident-init"
	crdPath := filepath.Join(resourcesPath, "crds")                 // "charts/trident/resources/crds"
	backendPath := filepath.Join(resourcesPath, "backends")         // "charts/trident/resources/backends"

	// get existing secret for svm in kube-system namespace
	existingSecret := &corev1.Secret{}
	err = a.client.Get(ctx, client.ObjectKey{Namespace: svmSeedSecretNamespace, Name: secretName}, existingSecret)
	if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	deployOpts := services.DeployTridentSecretsOptions{
		ProjectID:      projectId,
		ShootNamespace: a.shootNamespace,
		SecretName:     secretName,
		UserName:       string(existingSecret.Data["username"]),
		Password:       strfmt.Password(existingSecret.Data["password"]),
	}
	err = a.svnManager.DeployTridentSecretsInShootAsMR(ctx, deployOpts)
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
		if err := services.DeployResources(ctx, a.log, a.client, a.shootNamespace, tridentCRDsName, crdYamls); err != nil {
			return fmt.Errorf("failed to deploy Trident CRDs: %w", err)
		}
		// Wait for CRD Managed Resource to be Ready
		a.log.Info("Waiting for Trident CRDs managed resource to be ready", "name", tridentCRDsName)
		if err := managedresources.WaitUntilHealthyAndNotProgressing(ctx, a.client, a.shootNamespace, tridentCRDsName); err != nil {
			return fmt.Errorf("failed while waiting for Trident CRDs managed resource: %w", err)
		}
		a.log.Info("Trident CRDs managed resource is ready", "name", tridentCRDsName)
	}

	// 2. Load and Deploy Trident Init Resources
	a.log.Info("Loading Trident Init resources", "path", tridentInitPath)
	// Load only from the correct tridentInitPath, no exclusions needed as CRDs/Backends are separate dirs
	tridentInitYamls, err := services.LoadYAMLFiles(tridentInitPath)
	if err != nil {
		return fmt.Errorf("failed to load Trident Init resources from %s: %w", tridentInitPath, err)
	}
	if len(tridentInitYamls) > 0 {
		a.log.Info("Deploying Trident Init managed resource", "namespace", ex.Namespace, "name", tridentInitMR)
		err = services.DeployResources(ctx, a.log, a.client, ex.Namespace, tridentInitMR, tridentInitYamls)
		if err != nil {
			return fmt.Errorf("failed to create managed resources for Trident Init: %w", err)
		}
		a.log.Info("Trident Init managed resource deployment initiated", "name", tridentInitMR)
	}
	// 3. Process backend templates (only needs ProjectID now)
	a.log.Info("Processing backend templates", "path", backendPath)
	if err := services.ProcessBackendTemplates(a.log, chartPath, projectId, secretName, ontapConfig.SvmIpaddresses.ManagementLif); err != nil {
		return fmt.Errorf("failed to process backend templates: %w", err)
	}
	// 4. Load and Deploy Backend Resources
	a.log.Info("Loading Trident Backend resources", "path", backendPath)
	backendYamls, err := services.LoadYAMLFiles(backendPath) // Load only from the correct backendPath
	if err != nil {
		return fmt.Errorf("failed to load Trident Backend resources from %s: %w", backendPath, err)
	}
	if len(backendYamls) > 0 {
		a.log.Info("Deploying Trident Backends managed resource", "namespace", ex.Namespace, "name", tridentBackendsMR)
		err = services.DeployResources(ctx, a.log, a.client, ex.Namespace, tridentBackendsMR, backendYamls)
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
	// if err := managedresources.Delete(ctx, a.client, ex.Namespace, tridentInitMR, false); err != nil {
	// 	return err
	// }
	// if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, tridentInitMR); err != nil {
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

// ensureSvmForProject checks if an SVM for the given project ID exists, creates it if not.
func (a *actuator) ensureSvmForProject(ctx context.Context, svmIpaddresses ontapv1alpha1.SvmIpaddresses, projectId string, svmSeedSecretNamespace string) error {
	_, err := a.svnManager.GetSVMByName(projectId)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			a.log.Info("SVM not found, proceeding with creation", "projectId", projectId)

			svmOpts := services.CreateSVMOptions{
				ProjectID:              projectId,
				SvmIpaddresses:         svmIpaddresses,
				SvmSeedSecretNamespace: svmSeedSecretNamespace,
			}

			if err := a.svnManager.CreateSVM(ctx, svmOpts); err != nil {
				return fmt.Errorf("failed to ensure SVM for project %s: %w", projectId, err)
			}
			a.log.Info("Successfully created SVM", "projectId", projectId)
			return nil
		}
		// Handle other errors from GetSVMByName
		return fmt.Errorf("failed to check for existing SVM %s: %w", projectId, err)
	}

	a.log.Info("SVM already exists, skipping creation", "projectId", projectId)
	return nil
}

func (a *actuator) ensureClusterwideNetworkPolicy(ctx context.Context, svmIpaddresses ontapv1alpha1.SvmIpaddresses) error {

	var egressRules []firewallv1.EgressRule

	for _, datalif := range svmIpaddresses.DataLifs {
		rule := firewallv1.EgressRule{
			To: []networkingv1.IPBlock{
				{
					CIDR: datalif,
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: pointer.Pointer(corev1.ProtocolTCP),
					Port:     &intstr.IntOrString{IntVal: 4420},
				},
			},
		}
		egressRules = append(egressRules, rule)
	}

	managementRule := firewallv1.EgressRule{
		To: []networkingv1.IPBlock{
			{
				CIDR: svmIpaddresses.ManagementLif,
			},
		},
		Ports: []networkingv1.NetworkPolicyPort{
			{
				Protocol: pointer.Pointer(corev1.ProtocolTCP),
				Port:     &intstr.IntOrString{IntVal: 443},
			},
		},
	}
	egressRules = append(egressRules, managementRule)

	cwnp := &firewallv1.ClusterwideNetworkPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name: "allow-to-ontap",
		},
		Spec: firewallv1.PolicySpec{
			Description: "ontap storage access",
			Egress:      egressRules,
		},
	}

	// TODO decide if we take this approach or apply cwnp as templated yaml files

	if err := managedresources.CreateForShoot(
		ctx,
		a.client,
		"firewall",
		"ontap-access-clusterwidenetworkpolicy",
		true,
		"ClusterwideNetworkPolicy.metal-stack.io/v1",
		cwnp,
		true,
		nil,
	); err != nil {
		return fmt.Errorf("failed to create managed resource %s: %w", resourceName, err)
	}

	return nil
}
