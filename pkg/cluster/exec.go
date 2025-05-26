package cluster

import (
	"context"
	"fmt"

	"github.com/aifoundry-org/oxide-controller/pkg/util"
	"github.com/oxidecomputer/oxide.go/oxide"
)

// Execute execute the core functionality, which includes:
// 1. Verifying the project exists
// 2. Verifying the images exist
// 3. Verifying the cluster exists
// 4. Ensuring the worker nodes exist as desired
// 5. Returning the new kubeconfig, if any
func (c *Cluster) Execute(ctx context.Context) (newKubeconfig []byte, err error) {
	client, err := oxide.NewClient(c.oxideConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Oxide API client: %v", err)
	}

	projectID, err := ensureProjectExists(ctx, c.logger, client, c.config.ClusterProject)
	if err != nil {
		return nil, fmt.Errorf("project verification failed: %v", err)
	}
	if projectID != "" && projectID != c.config.ClusterProject {
		c.projectID = projectID
		c.logger.Infof("Using project ID: %s", c.projectID)
	}

	images, err := ensureImagesExist(ctx, c.logger, client, c.projectID, c.config.ImageParallelism, c.config.ControlPlaneSpec.Image, c.config.WorkerSpec.Image)
	if err != nil {
		return nil, fmt.Errorf("image verification failed: %v", err)
	}
	if len(images) != 2 {
		return nil, fmt.Errorf("expected 2 images, got %d", len(images))
	}
	c.logger.Infof("images %v", images)

	// control plane image and root disk size
	c.config.ControlPlaneSpec.Image = images[0]
	minSize := util.RoundUp(c.config.ControlPlaneSpec.Image.Size, GB)
	if c.config.ControlPlaneSpec.RootDiskSize == 0 {
		c.config.ControlPlaneSpec.RootDiskSize = minSize
	}
	if c.config.ControlPlaneSpec.RootDiskSize < minSize {
		return nil, fmt.Errorf("control plane root disk size %d is less than minimum image size %d", c.config.ControlPlaneSpec.RootDiskSize, minSize)
	}

	// worker image and root disk size
	c.config.WorkerSpec.Image = images[1]
	minSize = util.RoundUp(c.config.WorkerSpec.Image.Size, GB)
	if c.config.WorkerSpec.RootDiskSize == 0 {
		c.config.WorkerSpec.RootDiskSize = minSize
	}
	if c.config.WorkerSpec.RootDiskSize < minSize {
		return nil, fmt.Errorf("worker root disk size %d is less than minimum image size %d", c.config.WorkerSpec.RootDiskSize, minSize)
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
