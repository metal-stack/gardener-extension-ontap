package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gardener/gardener/pkg/utils/managedresources"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WIP
// LoadYAMLFiles takes a directory path and returns a slice of the raw YAML docs
func LoadYAMLFiles(dirPath string) (map[string][]byte, error) {
	result := make(map[string][]byte)

	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		filePath := filepath.Join(dirPath, f.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		result[f.Name()] = data
	}
	return result, nil
}

// DeployYAMLsToShoot loads the official yaml files to deploy the trident operator
func DeployYAMLsToShoot(
	ctx context.Context,
	seedClient client.Client,
	shootNamespace string,
	mrName string,
	yamlDirPath string,
) error {
	yamls, err := LoadYAMLFiles(yamlDirPath)
	if err != nil {
		return fmt.Errorf("loading yaml files failed: %w", err)
	}

	converted := make(map[string]string, len(yamls))
	i := 0
	for _, rawData := range yamls {
		safeKey := fmt.Sprintf("res-%d", i)
		converted[safeKey] = string(rawData)
		i++
	}

	if err := managedresources.Create(
		ctx,
		seedClient,
		shootNamespace,
		mrName,
		converted,
		false,
		"Shoot",
		nil,
		nil,
		nil,
		nil,
	); err != nil {
		return fmt.Errorf("create managed resource: %w", err)
	}

	return nil
}
