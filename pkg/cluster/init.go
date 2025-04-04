package cluster

import (
	"context"
	"fmt"

	"github.com/aifoundry-org/oxide-controller/pkg/util"
)

func (c *Cluster) Initialize(ctx context.Context, timeoutMinutes int) (newKubeconfig []byte, err error) {

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

	return c.ensureClusterExists(ctx, timeoutMinutes)
}
