package cluster

import (
	"context"
	"fmt"
)

func (c *Cluster) Initialize(ctx context.Context, kubeconfig, pubkey []byte, prefix string, controlPlaneCount int, controlPlaneImage, workerImage Image, controlPlaneMemoryGB, controlPlanCPUCount, timeoutMinutes int, secretName string) (newKubeconfig []byte, err error) {

	projectID, err := c.ensureProjectExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("project verification failed: %v", err)
	}
	if projectID != "" && projectID != c.projectID {
		c.projectID = projectID
		c.logger.Infof("Using project ID: %s", c.projectID)
	}

	if _, err := c.ensureImagesExist(ctx, controlPlaneImage, workerImage); err != nil {
		return nil, fmt.Errorf("image verification failed: %v", err)
	}

	return c.ensureClusterExists(ctx, kubeconfig, pubkey, prefix, controlPlaneCount, controlPlaneImage.Name, controlPlaneMemoryGB, controlPlanCPUCount, timeoutMinutes, secretName)
}
