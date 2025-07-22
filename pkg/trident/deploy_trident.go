package trident

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
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
			key := svmShootSecretFilename
			secret, ok := yamlBytes[key]
			if !ok {
				return fmt.Errorf("secret template file '%s' not found in loaded YAMLs for %s", key, resource.Name)
			}
			secretStr := string(secret)
			secretStr = strings.ReplaceAll(secretStr, "${SECRET_NAME}", *tridentValues.SeedsecretName)
			secretStr = strings.ReplaceAll(secretStr, "${NAMESPACE}", "kube-system") // not shoot namespace in seed, create this in kube-system ns in shoot
			secretStr = strings.ReplaceAll(secretStr, "${PROJECT_ID}", tridentValues.ProjectId)
			secretStr = strings.ReplaceAll(secretStr, "${USER_NAME}", tridentValues.Username)
			secretStr = strings.ReplaceAll(secretStr, "${PASSWORD}", tridentValues.Password)
			yamlBytes[key] = []byte(secretStr)
			log.Info("Templated SVM secret", "resource", resource.Name)

		case "trident-cwnp":
			for i, dataip := range tridentValues.SvmIpAddresses.DataLifs {
				key := cwnpFileName
				cwnp, ok := yamlBytes[key]
				if !ok {
					return fmt.Errorf("secret template file '%s' not found in loaded YAMLs for %s", key, resource.Name)
				}
				cwnpString := string(cwnp)
				cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_CIDR}", dataip)
				cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_NAME}", fmt.Sprintf("allow-nvme-port-%d", i))
				cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_PRTCL}", "TCP")
				cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_PORT}", "4420")
				yamlBytes[key] = []byte(cwnpString)
				log.Info("Templated SVM secret", "resource", resource.Name)
				err = deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, yamlBytes, resource.WaitForHealthy)
				if err != nil {
					return err
				}
			}

			managementip := tridentValues.SvmIpAddresses.ManagementLif
			key := cwnpFileName
			cwnp, ok := yamlBytes[key]
			if !ok {
				return fmt.Errorf("secret template file '%s' not found in loaded YAMLs for %s", key, resource.Name)
			}
			cwnpString := string(cwnp)
			cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_CIDR}", managementip)
			cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_NAME}", fmt.Sprintf("allow-ontap-mgmt-port"))
			cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_PRTCL}", "TCP")
			cwnpString = strings.ReplaceAll(cwnpString, "${CWNP_PORT}", "443")
			yamlBytes[key] = []byte(cwnpString)
			log.Info("Templated SVM secret", "resource", resource.Name)
			err = deployResources(ctx, log, k8sClient, tridentValues.Namespace, resource.Name, yamlBytes, resource.WaitForHealthy)
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
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
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
