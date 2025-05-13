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
// 4. Ensuring the worker nodes exist as desired
// 5. Returning the new kubeconfig, if any
func (c *Cluster) Execute(ctx context.Context) (newKubeconfig []byte, err error) {

	projectID, err := ensureProjectExists(ctx, c.logger, c.client, c.projectID)
	if err != nil {
		return nil, fmt.Errorf("project verification failed: %v", err)
	}
	if projectID != "" && projectID != c.projectID {
		c.projectID = projectID
		c.logger.Infof("Using project ID: %s", c.projectID)
	}

	images, err := ensureImagesExist(ctx, c.logger, c.client, c.projectID, c.imageParallelism, c.controlPlaneSpec.Image, c.workerSpec.Image)
	if err != nil {
		return nil, fmt.Errorf("image verification failed: %v", err)
	}
	if len(images) != 2 {
		return nil, fmt.Errorf("expected 2 images, got %d", len(images))
	}
	c.logger.Infof("images %v", images)

	// control plane image and root disk size
	c.controlPlaneSpec.Image = images[0]
	minSize := util.RoundUp(c.controlPlaneSpec.Image.Size, GB)
	if c.controlPlaneSpec.RootDiskSize == 0 {
		c.controlPlaneSpec.RootDiskSize = minSize
	}
	if c.controlPlaneSpec.RootDiskSize < minSize {
		return nil, fmt.Errorf("control plane root disk size %d is less than minimum image size %d", c.controlPlaneSpec.RootDiskSize, minSize)
	}

	// worker image and root disk size
	c.workerSpec.Image = images[1]
	minSize = util.RoundUp(c.workerSpec.Image.Size, GB)
	if c.workerSpec.RootDiskSize == 0 {
		c.workerSpec.RootDiskSize = minSize
	}
	if c.workerSpec.RootDiskSize < minSize {
		return nil, fmt.Errorf("worker root disk size %d is less than minimum image size %d", c.workerSpec.RootDiskSize, minSize)
	}

	newKubeconfig, err = c.ensureClusterExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("cluster verification failed: %v", err)
	}

	// ensure worker nodes as desired
	if _, err := c.EnsureWorkerNodes(ctx); err != nil {
		return nil, fmt.Errorf("failed to create worker nodes: %v", err)
	}
	return newKubeconfig, nil
}
