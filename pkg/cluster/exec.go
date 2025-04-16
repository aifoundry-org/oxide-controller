package cluster

import (
	"context"
	"fmt"

	"github.com/aifoundry-org/oxide-controller/pkg/util"
)

// Execute execute the core functionality, which includes:
// 1. Verifying the project exists
// 2. Verifying the images exist
// 3. Verifying the cluster exists
// 4. Ensuring the worker nodes are created as desired
// 5. Returning the new kubeconfig, if any
func (c *Cluster) Execute(ctx context.Context, timeoutMinutes int, kubeconfig []byte, kubeconfigOverwrite bool) (newKubeconfig []byte, err error) {

	projectID, err := ensureProjectExists(ctx, c.logger, c.client, c.projectID)
	if err != nil {
		return nil, fmt.Errorf("project verification failed: %v", err)
	}
	if projectID != "" && projectID != c.projectID {
		c.projectID = projectID
		c.logger.Infof("Using project ID: %s", c.projectID)
	}

	images, err := ensureImagesExist(ctx, c.logger, c.client, c.projectID, c.controlPlaneSpec.Image, c.workerSpec.Image)
	if err != nil {
		return nil, fmt.Errorf("image verification failed: %v", err)
	}
	if len(images) != 2 {
		return nil, fmt.Errorf("expected 2 images, got %d", len(images))
	}
	c.logger.Infof("images %v", images)
	c.controlPlaneSpec.Image = images[0]
	c.controlPlaneSpec.DiskSize = util.RoundUp(images[0].Size, GB)
	c.workerSpec.Image = images[1]
	c.workerSpec.DiskSize = util.RoundUp(images[0].Size, GB)

	newKubeconfig, err = c.ensureClusterExists(ctx, timeoutMinutes, kubeconfig, kubeconfigOverwrite)
	if err != nil {
		return nil, fmt.Errorf("cluster verification failed: %v", err)
	}

	// ensure worker nodes as desired
	if _, err := c.CreateWorkerNodes(ctx, c.workerCount.Load()); err != nil {
		return nil, fmt.Errorf("failed to create worker nodes: %v", err)
	}
	return newKubeconfig, nil
}
