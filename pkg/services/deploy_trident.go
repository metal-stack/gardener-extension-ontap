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
	ResourcesDir          = "resources"
	CRDsDir               = "crds"
	BackendsDir           = "backends"
	StorageClassFilename  = "storageclass.yaml"
	BackendConfigFilename = "backend-config.yaml"
	DefaultChartPath      = "charts/trident"
)

func LoadYAMLFiles(dirPath string, excludeSubdirs ...string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	// Check if the base directory exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		// If the directory doesn't exist, return an empty map and no error,
		// as it might be valid (e.g., no CRDs to load)
		fmt.Printf("Directory does not exist, returning empty map: %s\n", dirPath)
		return result, nil
	}
	excludeAbsPaths := make(map[string]bool)
	for _, subDir := range excludeSubdirs {
		if subDir != "" {
			excludeAbsPaths[filepath.Join(dirPath, subDir)] = true
		}
	}
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err // Propagate errors (e.g., permission issues)
		}
		// Check if the current path is an excluded directory
		if info.IsDir() {
			if _, excluded := excludeAbsPaths[path]; excluded {
				fmt.Printf("Excluding directory during YAML load: %s\n", path)
				return filepath.SkipDir // Don't process this directory further
			}
			return nil // Continue walking into non-excluded directories
		}
		// Process only YAML files
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		// Skip kustomization files
		if strings.HasSuffix(path, "kustomization.yaml") {
			return nil
		}
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			// Should generally not happen if walk starts correctly, but handle defensively
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
	// It's okay to return an empty map if no YAML files were found after exclusions
	if len(result) == 0 {
		fmt.Printf("No YAML files found in: %s (after potential exclusions)\n", dirPath)
	}
	return result, nil
}

// DeployResources deploys the provided YAML data as a managed resource.
func DeployResources(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	resourceName string,
	resourceYamls map[string][]byte, // Takes YAMLs directly
) error {
	// Check if there are any resources to deploy
	if len(resourceYamls) == 0 {
		fmt.Printf("No resources found to deploy for managed resource '%s' in namespace '%s'. Skipping deployment.\n", resourceName, namespace)
		return nil
	}

	fmt.Printf("Deploying %d YAML files for managed resource '%s' in namespace '%s'\n",
		len(resourceYamls), resourceName, namespace)

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

	fmt.Printf("Successfully initiated deployment for managed resource '%s'\n", resourceName)
	return nil
}

// ProcessBackendTemplates processes the backend templates with the given parameters
// and updates them in place in the chart directory
func ProcessBackendTemplates(
	log logr.Logger,
	chartPath string,
	projectId string,
	secretName string,
	dataLif string,
	managementLif string,
) error {
	// Create backend directory path
	backendDir := filepath.Join(chartPath, ResourcesDir, BackendsDir)

	// Ensure the backend directory exists
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		return fmt.Errorf("failed to create backends directory: %w", err)
	}

	// Read template files
	storageClassPath := filepath.Join(backendDir, StorageClassFilename)
	backendConfigPath := filepath.Join(backendDir, BackendConfigFilename)

	// Read backend config template (if it exists)
	backendConfigTemplate, err := os.ReadFile(backendConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read backend config template: %w", err)
	}

	// Process backend config if it exists
	if len(backendConfigTemplate) > 0 {
		backendConfigYaml := strings.ReplaceAll(string(backendConfigTemplate), "${PROJECT_ID}", projectId)
		backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${SECRET_NAME}", secretName)
		backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${DATA_LIF}", dataLif)
		backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${MANAGEMENT_LIF}", managementLif)

		if strings.Contains(backendConfigYaml, "${PROJECT_ID}") {
			log.Info("Warning: ${PROJECT_ID} placeholders still exist after replacement. Doing additional replacement.")
			backendConfigYaml = strings.ReplaceAll(backendConfigYaml, "${PROJECT_ID}", projectId)
		}

		if err := os.WriteFile(backendConfigPath, []byte(backendConfigYaml), 0600); err != nil {
			return fmt.Errorf("failed to write backend config file: %w", err)
		}
		log.Info("Updated backend config file with project values",
			"path", backendConfigPath,
			"projectId", projectId,
			"dataLif", dataLif)
	}
	// Log the storage class path
	log.Info("Storage class file ready for deployment", "path", storageClassPath)

	return nil
}
