package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gardener/gardener/pkg/utils/managedresources"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

	if err := managedresources.CreateForShoot(
		ctx,
		seedClient,
		shootNamespace,
		mrName,
		"trident",
		false,
		yamls,
	); err != nil {
		return fmt.Errorf("createForShoot managed resource: %w", err)
	}
	return nil
}
