package ontap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	"github.com/gardener/gardener/extensions/pkg/util"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/utils/managedresources"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/charts"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config/v1alpha1"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	"github.com/metal-stack/gardener-extension-ontap/pkg/imagevector"
	"github.com/metal-stack/gardener-extension-ontap/pkg/trident"
	"github.com/metal-stack/metal-lib/pkg/tag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimelog "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	"github.com/metal-stack/ontap-go/api/client/cluster"
	ontapclient "github.com/metal-stack/ontap-go/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	secretNameFormat = "ontap-svm-%s-credentials"
)

// FIXME here the logic to deploy the trident operator

type actuator struct {
	ontap                *ontapv1.Ontap
	client               client.Client
	shootNamespace       string
	decoder              runtime.Decoder
	config               config.ControllerConfiguration
	chartRendererFactory extensionscontroller.ChartRendererFactory
}

// NewActuator returns an actuator responsible for Extension resources.
func NewActuator(ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (extension.Actuator, error) {
	ontapClient, err := createAdminClient(ctx, config)
	if err != nil {
		return nil, err
	}

	return &actuator{
		ontap:                ontapClient,
		client:               mgr.GetClient(),
		decoder:              serializer.NewCodecFactory(mgr.GetScheme()).UniversalDeserializer(),
		config:               config,
		chartRendererFactory: extensionscontroller.ChartRendererFactoryFunc(util.NewChartRendererForShoot),
	}, nil
}

func createAdminClient(ctx context.Context, config config.ControllerConfiguration) (*ontapv1.Ontap, error) {
	err := config.Validate()
	if err != nil {
		return nil, err
	}

	var (
		log          = runtimelog.Log.WithName(ControllerName)
		ontapConfigs []ontapclient.Config
	)

	for _, cluster := range config.Clusters {
		clusterClientConfig := ontapclient.Config{
			AdminUser:     cluster.Username,
			AdminPassword: cluster.Password,
			Host:          cluster.IPAddress,
			InsecureTLS:   true,
		}

		log.Info("adding cluster config", "cluster", cluster.Name, "user", cluster.Username, "ip", cluster.IPAddress)

		ontapConfigs = append(ontapConfigs, clusterClientConfig)
	}

	metroClusterClient, err := ontapclient.NewMetroClusterClient(ontapConfigs)
	if err != nil {
		return nil, err
	}

	// Get statistics of cluster, can be updated in the future to check the state or capacity to choose cluster for the client
	for _, client := range *metroClusterClient {
		cgparams := cluster.NewClusterGetParamsWithContext(ctx)
		cgok, err := client.Cluster.ClusterGet(cgparams, nil)
		if err != nil {
			return nil, err
		}
		clusterResponse := cgok.Payload

		log.Info("Successfully connected to ONTAP cluster", "cluster", *clusterResponse.Name, "statistics", *clusterResponse.Statistics)

		// for now just return the first client
		// TODO create logic to switch between clients
		if *clusterResponse.Statistics.Status == "ok" {
			return &client, nil
		}
	}

	return nil, fmt.Errorf("couldn't initialize admin client")
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

	log.Info("raw provideconfig", "tridentconfig", string(ex.Spec.ProviderConfig.Raw))

	// TODO: should be validated by an admission controller, not here
	if err := ontapConfig.Validate(); err != nil {
		return fmt.Errorf("invalid trident config: %w", err)
	}

	namespace := ex.GetNamespace()

	cluster, err := extensionscontroller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return err
	}

	// TODO: this value has to come from the extension object, not some arbitrary cluster tag
	var projectTag tag.TagMap = cluster.Shoot.Annotations
	projectId, ok := projectTag.Value(tag.ClusterProject)
	if !ok || projectId == "" {
		return fmt.Errorf("no project ID found in shoot annotations")
	}

	// TODO: factor out into function
	log.Info("Found project ID in shoot annotations", "projectId", projectId)
	// Project id "-" to be replaced, ontap doesn't like "-"
	projectId = strings.ReplaceAll(projectId, "-", "")
	// ontap wants a letter or _ as prefix
	projectId = "p" + projectId

	// TODO: who cleans up this secret? what if there is a second seed in this partition? should be returned by ensure function?
	svmSeedSecretNamespace := "kube-system"

	log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", svmSeedSecretNamespace, "managementLifIp", ontapConfig.SvmIpaddresses.ManagementLif, "dataLifIps", ontapConfig.SvmIpaddresses.DataLifs)
	if err := a.ensureSvmForProject(ctx, log, ontapConfig.SvmIpaddresses, projectId, svmSeedSecretNamespace); err != nil {
		return err
	}

	seedsecretName := fmt.Sprintf(secretNameFormat, projectId)
	log.Info("Using credentials from secret in shoot cluster", "secretName", seedsecretName, "namespace", "kube-system")

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
	tridentOperatorImage, err := imagevector.ImageVector().FindImage("trident-operator")
	if err != nil {
		return err
	}
	busyboxImage, err := imagevector.ImageVector().FindImage("busybox")
	if err != nil {
		return err
	}

	values := map[string]any{
		"images": map[string]any{
			"trident-operator": tridentOperatorImage,
			"busybox":          busyboxImage,
		},
		"projectID":       projectId,
		"managementLifIP": ontapConfig.SvmIpaddresses.ManagementLif,
		"dataLifs":        ontapConfig.SvmIpaddresses.DataLifs,
		"username":        string(username),
		"password":        string(password),
	}

	version := cluster.Shoot.Spec.Kubernetes.Version

	chartRenderer, err := a.chartRendererFactory.NewChartRendererForShoot(version)
	if err != nil {
		return fmt.Errorf("could not create chart renderer for shoot '%s': %w", namespace, err)
	}

	release, err := chartRenderer.RenderEmbeddedFS(charts.InternalChart, filepath.Join(charts.InternalChartsPath, "shoot-trident"), "shoot-trident", metav1.NamespaceSystem, values)
	if err != nil {
		return err
	}

	data := map[string][]byte{"config.yaml": release.Manifest()}
	if err := managedresources.CreateForShoot(ctx, a.client, namespace, v1alpha1.ShootOntapResourceName, "extension-ontap", false, data); err != nil {
		return err
	}

	log.Info("ONTAP extension reconciliation completed successfully")

	return nil
}

// Delete the Extension resource.
func (a *actuator) Delete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	if err := managedresources.Delete(ctx, a.client, ex.Namespace, v1alpha1.ShootOntapResourceName, false); err != nil {
		return err
	}
	if err := managedresources.WaitUntilDeleted(ctx, a.client, ex.Namespace, v1alpha1.ShootOntapResourceName); err != nil {
		return err
	}

	log.Info("ManagedResource for Trident operator successfully deleted")

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
func (a *actuator) ensureSvmForProject(ctx context.Context, log logr.Logger, SvmIpaddresses ontapv1alpha1.SvmIpaddresses, projectId string, svmSeedSecretNamespace string) error {
	svnManager := trident.NewSvmManager(log, a.ontap, a.client)

	_, err := svnManager.GetSVMByName(ctx, projectId)
	if err != nil {
		if errors.Is(err, trident.ErrSvmNotFound) {
			log.Info("SVM not found, proceeding with creation", "projectId", projectId)

			svmOpts := trident.CreateSVMOptions{
				ProjectID:              projectId,
				SvmIpaddresses:         SvmIpaddresses,
				SvmSeedSecretNamespace: svmSeedSecretNamespace,
			}

			if err := svnManager.CreateSVM(ctx, svmOpts); err != nil {
				return fmt.Errorf("failed to ensure SVM for project %s: %w", projectId, err)
			}
			log.Info("Successfully created SVM", "projectId", projectId)
			return nil
		}

		// Handle other errors from GetSVMByName
		return fmt.Errorf("failed to check for existing SVM %s: %w", projectId, err)
	}

	log.Info("SVM already exists, skipping creation", "projectId", projectId)

	return nil
}
