package ontap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
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
)

// FIXME here the logic to deploy the trident operator

type actuator struct {
	ontap          *ontapv1.Ontap
	client         client.Client
	shootNamespace string
	decoder        runtime.Decoder
	config         config.ControllerConfiguration
}

// NewActuator returns an actuator responsible for Extension resources.
func NewActuator(ctx context.Context, mgr manager.Manager, config config.ControllerConfiguration) (extension.Actuator, error) {
	ontapClient, err := createAdminClient(ctx, config)
	if err != nil {
		return nil, err
	}

	return &actuator{
		ontap:   ontapClient,
		client:  mgr.GetClient(),
		decoder: serializer.NewCodecFactory(mgr.GetScheme()).UniversalDeserializer(),
		config:  config,
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

	log.Info("Shoot annotations", "annotations", shoot.Annotations)
	var projectTag tag.TagMap = shoot.Annotations
	projectId, ok := projectTag.Value(tag.ClusterProject)
	if !ok || projectId == "" {
		return fmt.Errorf("no project ID found in shoot annotations")
	}

	log.Info("Found project ID in shoot annotations", "projectId", projectId)
	// Project id "-" to be replaced, ontap doesn't like "-"
	projectId = strings.ReplaceAll(projectId, "-", "")
	// ontap wants a letter or _ as prefix
	projectId = "p" + projectId

	svmSeedSecretNamespace := "kube-system"

	log.Info("Using project ID for SVM creation", "projectId", projectId, "namespace", svmSeedSecretNamespace, "managementLifIp", ontapConfig.SvmIpaddresses.ManagementLif, "dataLifIps", ontapConfig.SvmIpaddresses.DataLifs)
	if err := a.ensureSvmForProject(ctx, log, ontapConfig.SvmIpaddresses, projectId, svmSeedSecretNamespace); err != nil {
		return err
	}

	seedsecretName := fmt.Sprintf(trident.SecretNameFormat, projectId)
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

	svmIpAddresses := ontapv1alpha1.SvmIpaddresses{
		DataLifs:      ontapConfig.SvmIpaddresses.DataLifs,
		ManagementLif: ontapConfig.SvmIpaddresses.ManagementLif,
	}

	tridentValues := trident.DeployTridentValues{
		Namespace:      a.shootNamespace,
		ProjectId:      projectId,
		SeedsecretName: &seedsecretName,
		SvmIpAddresses: svmIpAddresses,
		Username:       string(username),
		Password:       string(password),
	}
	err := trident.DeployTrident(ctx, log, a.client, tridentValues)
	if err != nil {
		return err
	}

	log.Info("ONTAP extension reconciliation completed successfully")

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
func (a *actuator) ensureSvmForProject(ctx context.Context, log logr.Logger, SvmIpaddresses ontapv1alpha1.SvmIpaddresses, projectId string, svmSeedSecretNamespace string) error {
	svnManager := trident.NewSvmManager(log, a.ontap, a.client)

	_, err := svnManager.GetSVMByName(ctx, projectId)
	if err != nil {
		if errors.Is(err, trident.ErrNotFound) {
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
