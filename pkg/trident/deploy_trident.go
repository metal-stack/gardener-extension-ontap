package trident

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"

	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/charts/trident/resources/cwnps"
	"github.com/metal-stack/gardener-extension-ontap/charts/trident/resources/secrets"
	"github.com/metal-stack/gardener-extension-ontap/charts/trident/resources/webhook"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Constants for paths and names
const (
	// Constants for directory names
	storageClassFilename   = "storageclass.yaml"
	backendConfigFilename  = "backend-config.yaml"
	svmShootSecretFilename = "svm-shoot-secret.yaml"
	cwnpFileName           = "cwnp.yaml"

	//Why hardcod
	tridentCRDsName        string = "trident-crds"
	tridentInitMR          string = "trident-init"
	tridentBackendsMR      string = "trident-backends"
	tridentSvmSecret       string = "trident-svm-secret"
	tridentCwnp            string = "trident-cwnp"
	tridentWebhook         string = "trident-webhook"
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
	cwnpPath        = filepath.Join(resourcesPath, "cwnps")
	webhookPath     = filepath.Join(resourcesPath, "webhook")

	tridentResources = []TridentResource{
		{Name: tridentInitMR, Path: tridentInitPath, WaitForHealthy: false},
		{Name: tridentCRDsName, Path: crdPath, WaitForHealthy: true},
		{Name: tridentBackendsMR, Path: backendPath, WaitForHealthy: false},
		{Name: tridentSvmSecret, Path: svmSecretsPath, WaitForHealthy: false},
		{Name: tridentCwnp, Path: cwnpPath, WaitForHealthy: false},
		{Name: tridentWebhook, Path: webhookPath, WaitForHealthy: false},
	}
)

type DeployTridentValues struct {
	Namespace        string
	ProjectId        string
	SeedsecretName   *string
	SvmIpAddresses   ontapv1alpha1.SvmIpaddresses
	Username         string
	Password         string
	WebhookNamespace string
	WebhookCABundle  string
}

type TridentResource struct {
	Name           string
	Path           string
	WaitForHealthy bool
}

// DeployTrident deploys Trident using the provided values and resources.
func DeployTrident(ctx context.Context, log logr.Logger, k8sClient client.Client, tridentValues DeployTridentValues) error {
	for _, resource := range tridentResources {
		log.Info("loading YAML files for resource", "resource", resource.Name)
		yamlBytes, err := loadYAMLFiles(resource.Path)
		if err != nil {
			return err
		}
		log.Info("before switch case in deploy trident", "resourcename", resource.Name)

		// FIXME Will be changed to go template
		switch resource.Name {
		case tridentBackendsMR:
			key := backendConfigFilename
			config, ok := yamlBytes[key]
			if !ok {
				return fmt.Errorf("backend config file '%s' not found in loaded YAMLs for %s", key, resource.Name)
			}
			configStr := string(config)
			configStr = strings.ReplaceAll(configStr, "${PROJECT_ID}", tridentValues.ProjectId)
			configStr = strings.ReplaceAll(configStr, "${SECRET_NAME}", *tridentValues.SeedsecretName)
			configStr = strings.ReplaceAll(configStr, "${MANAGEMENT_LIF_IP}", tridentValues.SvmIpAddresses.ManagementLif)
			yamlBytes[key] = []byte(configStr)
			log.Info("Templated backend config", "resource", resource.Name)

		case tridentSvmSecret:
			secretsData := secrets.Secrets{
				Name:      *tridentValues.SeedsecretName,
				Namespace: "kube-system",
				Project:   tridentValues.ProjectId,
				Username:  tridentValues.Username,
				Password:  tridentValues.Password,
			}
			rendered, err := secrets.Parse(secretsData)
			if err != nil {
				return err
			}
			resourceToDeploy := map[string][]byte{
				svmShootSecretFilename: []byte(rendered),
			}
			log.Info("templated secret", "resource", resource.Name, "input", secretsData, "output", rendered)
			err = deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, resourceToDeploy, resource.WaitForHealthy)
			if err != nil {
				return err
			}
			continue

		case tridentCwnp:
			log.Info("case trident cwnp inside deploy", "cwnp", tridentCwnp)
			var firewallNamespace corev1.Namespace
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(&corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: "firewall"}}), &firewallNamespace)
			if err != nil {
				if errors.IsNotFound(err) {
					log.Info("firewall ns doesn't exist, not deploying cwnps")
					break
				}
				return err
			}
			cwnp := cwnps.CWNP{
				ManagementLif: tridentValues.SvmIpAddresses.ManagementLif,
				DataLifs:      tridentValues.SvmIpAddresses.DataLifs,
			}
			rendered, err := cwnps.ParseCWNP(cwnp)
			if err != nil {
				return err
			}
			resourceToDeploy := map[string][]byte{
				cwnpFileName: []byte(rendered),
			}
			log.Info("templated cwnps", "resource", resource.Name, "input", cwnp, "output", rendered)
			err = deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, resourceToDeploy, resource.WaitForHealthy)
			if err != nil {
				return err
			}
			continue

		case tridentWebhook:
			// Template the webhook using Go templating
			webhookConfig := webhook.Webhook{
				WebhookNamespace: tridentValues.WebhookNamespace,
				CABundle:         tridentValues.WebhookCABundle,
			}
			rendered, err := webhook.Parse(webhookConfig)
			if err != nil {
				return fmt.Errorf("failed to template webhook config for %s: %w", resource.Name, err)
			}
			resourceToDeploy := map[string][]byte{
				"mutating-webhook.yaml": []byte(rendered),
			}
			if err := deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, resourceToDeploy, resource.WaitForHealthy); err != nil {
				return err
			}
			continue
		}

		err = deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, yamlBytes, resource.WaitForHealthy)
		if err != nil {
			return err
		}
	}
	return nil
}

func DeleteManagedResources(ctx context.Context, log logr.Logger, client client.Client, ex *extensionsv1alpha1.Extension) error {
	// Phase 1: Clean up Trident-created resources with finalizers
	if err := cleanupTridentResources(ctx, log, client); err != nil {
		log.Error(err, "failed to cleanup Trident resources, continuing with managed resource deletion")
		// Continue with deletion even if cleanup fails
	}

	resources := slices.Clone(tridentResources)
	slices.Reverse(resources)

	// Phase 2: Delete managed resources in reverse order
	for _, resource := range tridentResources {
		if err := managedresources.Delete(ctx, client, ex.Namespace, resource.Name, false); err != nil {
			log.Error(err, "unable to delete managedresource", "resource", resource.Name)
			return err
		}
		log.Info("managedresource deleted successfully", "resource", resource.Name)
	}

	log.Info("all managed resources successfully deleted.")

	return nil
}

// loadYAMLFiles walks the given directory path and reads all YAML files,
// returning them in a map where the key is the relative path with '/' replaced by '.'.
func loadYAMLFiles(dirPath string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	// Check if the base directory exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return result, fmt.Errorf("directory does not exist, returning empty map: %s", dirPath)
	}
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err // Propagate errors (e.g., permission issues)
		}
		if info.IsDir() {
			if path == dirPath {
				return nil
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".tpl") {
			return nil
		}
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		key := strings.ReplaceAll(relPath, "/", ".")
		result[key] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking %s: %w", dirPath, err)
	}
	if len(result) == 0 {
		return result, fmt.Errorf("no YAML files found in: %s", dirPath)
	}
	return result, nil
}

// deployResources deploys the provided YAML data as a managed resource.
func deployResources(
	ctx context.Context,
	log logr.Logger,
	k8sClient client.Client,
	namespace string,
	resourceName string,
	resourceYamls map[string][]byte, // Takes YAMLs directly
	waitForHealthy bool,
) error {
	// Check if there are any resources to deploy
	if len(resourceYamls) == 0 {
		log.Info("no resources found to deploy for managed resource, skipping deployment", "name", resourceName, "namespace", namespace)
		return nil
	}

	log.Info("deploying YAML files for managed resource", "name", resourceName, "namespace", namespace, "count", len(resourceYamls))

	// Create the managed resource
	if err := managedresources.CreateForShoot(
		ctx,
		k8sClient,
		namespace,
		resourceName,
		"kube-system", // Origin label
		false,
		resourceYamls,
	); err != nil {
		return fmt.Errorf("failed to create managed resource %s: %w", resourceName, err)
	}
	if waitForHealthy {
		if err := managedresources.WaitUntilHealthyAndNotProgressing(ctx, k8sClient, namespace, resourceName); err != nil {
			return fmt.Errorf("failed while waiting for Trident CRDs managed resource: %w", err)
		}
	}

	log.Info("successfully initiated deployment for managed resource", "name", resourceName)
	return nil
}

// cleanupTridentResources deletes Trident-created resources with finalizers
func cleanupTridentResources(ctx context.Context, log logr.Logger, k8sClient client.Client) error {
	// List of Trident resource types that may have finalizers
	tridentResources := []struct {
		gvk    schema.GroupVersionKind
		plural string
	}{
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentActionMirrorUpdate",
			},
			plural: "tridentactionmirrorupdates",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentActionSnapshotRestore",
			},
			plural: "tridentactionsnapshotrestores",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentBackendConfig",
			},
			plural: "tridentbackendconfigs",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentBackend",
			},
			plural: "tridentbackends",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentConfigurator",
			},
			plural: "tridentconfigurators",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentMirrorRelationship",
			},
			plural: "tridentmirrorrelationships",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentNode",
			},
			plural: "tridentnodes",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentOrchestrator",
			},
			plural: "tridentorchestrators",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentSnapshotInfo",
			},
			plural: "tridentsnapshotinfos",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentSnapshot",
			},
			plural: "tridentsnapshots",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentStorageClass",
			},
			plural: "tridentstorageclasses",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentTransaction",
			},
			plural: "tridenttransactions",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentVersion",
			},
			plural: "tridentversions",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentVolumePublication",
			},
			plural: "tridentvolumepublications",
		},
		{
			gvk: schema.GroupVersionKind{
				Group:   "trident.netapp.io",
				Version: "v1",
				Kind:    "TridentVolumeReference",
			},
			plural: "tridentvolumereferences",
		},
	}

	for _, resource := range tridentResources {
		log.Info("cleaning up Trident resources", "type", resource.gvk.Kind)

		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(resource.gvk)

		if err := k8sClient.List(ctx, list, client.InNamespace("kube-system")); err != nil {
			log.Info("skipping resource type", "type", resource.gvk.Kind, "reason", err.Error())
			return fmt.Errorf("failed to list %s: %w", resource.gvk.Kind, err)
		}

		// Delete each resource and remove finalizers if needed
		for _, item := range list.Items {
			log.Info("deleting resource", "kind", resource.gvk.Kind, "name", item.GetName())

			// First try normal deletion
			if err := k8sClient.Delete(ctx, &item); err != nil {
				// Has to be tested, if it blocks,fails or how it behaves
				// Actually have to trigger a delete before the finalizer gets added
				log.Error(err, "failed to delete resource", "kind", resource.gvk.Kind, "name", item.GetName())
			}

			// Remove the finalizers
			current := &unstructured.Unstructured{}
			current.SetGroupVersionKind(resource.gvk)
			err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: item.GetNamespace(),
				Name:      item.GetName(),
			}, current)
			if err != nil {
				return err
			}
			if len(current.GetFinalizers()) > 0 {
				log.Info("removing finalizers from resource", "kind", resource.gvk.Kind, "name", item.GetName())
				current.SetFinalizers([]string{})
				if err := k8sClient.Update(ctx, current); err != nil {
					log.Error(err, "failed to remove finalizers", "kind", resource.gvk.Kind, "name", item.GetName())
				}
			}
		}
	}

	return nil
}
