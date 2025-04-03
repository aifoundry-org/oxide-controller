package cluster

import (
	"context"
	"fmt"

	"github.com/oxidecomputer/oxide.go/oxide"
)

func Initialize(ctx context.Context, kubeconfig, pubkey []byte, oxideClient *oxide.Client, projectID, prefix string, controlPlaneCount int, controlPlaneImage, workerImage Image, controlPlaneMemoryGB, controlPlanCPUCount, timeoutMinutes int, secretName string) (newKubeconfig []byte, err error) {

	projectID, err = ensureProjectExists(ctx, oxideClient, projectID)
	if err != nil {
		return nil, fmt.Errorf("project verification failed: %v", err)
	}

	if _, err := ensureImagesExist(ctx, oxideClient, projectID, controlPlaneImage, workerImage); err != nil {
		return nil, fmt.Errorf("image verification failed: %v", err)
	}

	return ensureClusterExists(ctx, oxideClient, projectID, kubeconfig, pubkey, prefix, controlPlaneCount, controlPlaneImage.Name, controlPlaneMemoryGB, controlPlanCPUCount, timeoutMinutes, secretName)
}
