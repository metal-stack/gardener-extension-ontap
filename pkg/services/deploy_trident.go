package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Constants for paths and names
const (
	// Constants for directory names
	storageClassFilename  = "storageclass.yaml"
	backendConfigFilename = "backend-config.yaml"
)

// LoadYAMLFiles walks the given directory path and reads all YAML files,
// returning them in a map where the key is the relative path with '/' replaced by '.'.
func LoadYAMLFiles(dirPath string) (map[string][]byte, error) {
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

// DeployResources deploys the provided YAML data as a managed resource.
func DeployResources(
	ctx context.Context,
	log logr.Logger,
	k8sClient client.Client,
	namespace string,
	resourceName string,
	resourceYamls map[string][]byte, // Takes YAMLs directly
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
	// this was checked before only for the crd deployment, will have to check if this gives issues when checking for other mr deployments aswell
	if err := managedresources.WaitUntilHealthyAndNotProgressing(ctx, k8sClient, namespace, resourceName); err != nil {
		return fmt.Errorf("failed while waiting for Trident CRDs managed resource: %w", err)
	}
	log.Info("successfully initiated deployment for managed resource", "name", resourceName)
	return nil
}

// ProcessBackendTemplates reads backend templates, replaces placeholders, and writes the results back.
// It now only replaces PROJECT_ID and SECRET_NAME as LIFs are handled by service FQDNs directly in the template.
func ProcessBackendTemplates(log logr.Logger, chartPath, projectId, secretName, managementIp string) error {
	backendTemplateDir := filepath.Join(chartPath, "resources", "backends")
	log.Info("Processing backend templates", "directory", backendTemplateDir)

	// Ensure the backend directory exists
	if err := os.MkdirAll(backendTemplateDir, 0755); err != nil {
		return fmt.Errorf("failed to create backends directory %s: %w", backendTemplateDir, err)
	}

	// Read template files
	backendConfigPath := filepath.Join(backendTemplateDir, backendConfigFilename)

	// Read backend config template (if it exists)
	backendConfigTemplate, err := os.ReadFile(backendConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read backend config template: %w", err)
	}

	// Process backend config if it exists
	if len(backendConfigTemplate) > 0 {
		backendConfigYaml := strings.ReplaceAll(string(backendConfigTemplate), "${PROJECT_ID}", projectId)
		backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${SECRET_NAME}", secretName)

		if strings.Contains(backendConfigYaml, "${PROJECT_ID}") {
			log.Info("Warning: ${PROJECT_ID} placeholders still exist after replacement. Doing additional replacement.")
			backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${PROJECT_ID}", projectId)
			backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${MANAGEMENT_LIF_IP}", managementIp)
		}

		if err := os.WriteFile(backendConfigPath, []byte(backendConfigYaml), 0600); err != nil {
			return fmt.Errorf("failed to write backend config file: %w", err)
		}
		log.Info("Updated backend config file with project values",
			"path", backendConfigPath,
			"projectId", projectId,
			"secretName", secretName)
	}

	return nil
}

func DeployTrident(ctx context.Context, log logr.Logger, k8sClient client.Client, namespace, projectId, secretName, managementLifIp string, tridentRessourceToDeploy map[string]string) error {
	for resourceName, yamlPath := range tridentRessourceToDeploy {
		yamlBytes, err := LoadYAMLFiles(yamlPath)
		if err != nil {
			return err
		}

		if resourceName == "trident-backends" {
			if err := ProcessBackendTemplates(log, yamlPath, projectId, secretName, managementLifIp); err != nil {
				return fmt.Errorf("failed to process backend templates: %w", err)
			}
		}

		log.Info("deploying %s as managedressource", resourceName)
		err = DeployResources(ctx, log, k8sClient, namespace, resourceName, yamlBytes)
		if err != nil {
			return err
		}

	}
	return nil
}
