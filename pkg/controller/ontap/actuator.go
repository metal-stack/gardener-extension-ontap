package ontap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"

	"github.com/gardener/gardener/pkg/apis/core/install"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	"github.com/metal-stack/gardener-extension-ontap/pkg/trident"
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
	tridentCRDsName        string = "trident-crds"
	tridentInitMR          string = "trident-init"
	tridentBackendsMR      string = "trident-backends"
	tridentSvmSecret       string = "trident-svm-secret"
	svmSeedSecretNamespace string = "kube-system"

	defaultChartPath = "charts/trident"
)

var (
	chartPath       = defaultChartPath
	resourcesPath   = filepath.Join(chartPath, "resources")
	tridentInitPath = filepath.Join(resourcesPath, "trident-init")
	crdPath         = filepath.Join(resourcesPath, "crds")
	backendPath     = filepath.Join(resourcesPath, "backends")
	svmSecretsPath  = filepath.Join(resourcesPath, "secrets")

	tridentResourceToDeploy = []trident.TridentResource{
		{Name: tridentInitMR, Path: tridentInitPath, WaitForHealthy: false},
		{Name: tridentCRDsName, Path: crdPath, WaitForHealthy: true},
		{Name: tridentBackendsMR, Path: backendPath, WaitForHealthy: false},
		{Name: tridentSvmSecret, Path: svmSecretsPath, WaitForHealthy: false},
	}
)

type actuator struct {
	log            logr.Logger
	ontap          *ontapv1.Ontap
	client         client.Client
	svnManager     *trident.SvnManager
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

	svnManager := trident.NewSvnManager(log, ontap, client)
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

	var numSVMs int64
	if result != nil && result.Payload != nil && result.Payload.NumRecords != nil {
		numSVMs = *result.Payload.NumRecords
	}
	log.Info("Successfully connected to ONTAP. Found existing SVMs", "svms", numSVMs)

	return ontap, nil
}

// Reconcile handles extension creation and updates.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	a.shootNamespace = ex.Namespace

	if ex.Spec.ProviderConfig == nil {
		return fmt.Errorf("provider config is nil")
	}

	ontapConfig := &ontapv1alpha1.TridentConfig{}
	if _, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, ontapConfig); err != nil {
		return fmt.Errorf("failed to decode provider config: %w", err)
	}

	if err := ontapConfig.Validate(); err != nil {
		return fmt.Errorf("invalid trident config: %w", err)
	}

	cluster := &extensionsv1alpha1.Cluster{}
	if err := a.client.Get(ctx, client.ObjectKey{Name: ex.Namespace}, cluster); err != nil {
		return fmt.Errorf("failed to get cluster object: %w", err)
	}
	if cluster.Spec.Shoot.Raw == nil {
		return fmt.Errorf("cluster.spec.shoot.raw is nil")
	}

	shoot := &gardencorev1beta1.Shoot{}
	if _, _, err := a.decoder.Decode(cluster.Spec.Shoot.Raw, nil, shoot); err != nil {
		log.Error(err, "failed to decode shoot, continuing with partial shoot object")
	}

	a.log.Info("Shoot annotations", "annotations", shoot.Annotations)
	var projectTag tag.TagMap = shoot.Annotations
	projectId, ok := projectTag.Value(tag.ClusterProject)
	if !ok || projectId == "" {
		return fmt.Errorf("no project ID found in shoot annotations")
	}

	a.log.Info("Found project ID in shoot annotations", "projectId", projectId)
	// Project id "-" to be replaced, ontap doesn't like "-"
	projectId = strings.ReplaceAll(projectId, "-", "")

	a.log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", svmSeedSecretNamespace, "managementLifIp", ontapConfig.SvmIpaddresses.ManagementLif, "dataLifIps", ontapConfig.SvmIpaddresses.DataLifs)
	if err := a.ensureSvmForProject(ctx, ontapConfig.SvmIpaddresses, projectId, svmSeedSecretNamespace); err != nil {
		return err
	}

	seedsecretName := fmt.Sprintf(trident.SecretNameFormat, projectId)
	a.log.Info("Using credentials from secret in shoot cluster", "secretName", seedsecretName, "namespace", "kube-system")

	// get existing secret for svm in kube-system namespace
	existingSecret := &corev1.Secret{}
	if err := a.client.Get(ctx, client.ObjectKey{Namespace: svmSeedSecretNamespace, Name: seedsecretName}, existingSecret); err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	username, ok := existingSecret.Data["username"]
	if !ok {
		return fmt.Errorf("username not found in seed secret, secretname:%s", seedsecretName)
	}
	password, ok := existingSecret.Data["password"]
	if !ok {
		return fmt.Errorf("password not found in seed secret secretname:%s", seedsecretName)
	}

	tridentValues := trident.DeployTridentValues{
		Namespace:       a.shootNamespace,
		ProjectId:       projectId,
		SeedsecretName:  &seedsecretName,
		ManagementLifIp: ontapConfig.SvmIpaddresses.ManagementLif,
		Username:        string(username),
		Password:        string(password),
	}
	err := trident.DeployTrident(ctx, a.log, a.client, tridentValues, tridentResourceToDeploy)
	if err != nil {
		return err
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
func (a *actuator) ensureSvmForProject(ctx context.Context, SvmIpaddresses ontapv1alpha1.SvmIpaddresses, projectId string, svmSeedSecretNamespace string) error {
	_, err := a.svnManager.GetSVMByName(projectId)
	if err != nil {
		if errors.Is(err, trident.ErrNotFound) {
			a.log.Info("SVM not found, proceeding with creation", "projectId", projectId)

			svmOpts := trident.CreateSVMOptions{
				ProjectID:              projectId,
				SvmIpaddresses:         SvmIpaddresses,
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
