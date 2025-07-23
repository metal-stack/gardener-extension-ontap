package trident

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-ontap/charts/trident/resources/cwnps"
	"github.com/metal-stack/gardener-extension-ontap/charts/trident/resources/secrets"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Constants for paths and names
const (
	// Constants for directory names
	storageClassFilename   = "storageclass.yaml"
	backendConfigFilename  = "backend-config.yaml"
	svmShootSecretFilename = "svm-shoot-secret.yaml"
	cwnpFileName           = "cwnp.yaml"
)

type DeployTridentValues struct {
	Namespace      string
	ProjectId      string
	SeedsecretName *string
	SvmIpAddresses ontapv1alpha1.SvmIpaddresses
	Username       string
	Password       string
}

type TridentResource struct {
	Name           string
	Path           string
	WaitForHealthy bool
}

// DeployTrident deploys Trident using the provided values and resources.
func DeployTrident(ctx context.Context, log logr.Logger, k8sClient client.Client, tridentValues DeployTridentValues, tridentResourceToDeploy []TridentResource) error {
	for _, resource := range tridentResourceToDeploy {
		log.Info("loading YAML files for resource", "resource", resource.Name)
		yamlBytes, err := loadYAMLFiles(resource.Path)
		if err != nil {
			return err
		}

		// FIXME Will be changed to go template
		switch resource.Name {
		case "trident-backends":
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

		case "trident-svm-secret":
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
			log.Info("templated SVM secret", "resource", resource.Name)
			return nil

		case "trident-cwnp":
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
			return nil
		}

		err = deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, yamlBytes, resource.WaitForHealthy)
		if err != nil {
			return err
		}
	}
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
