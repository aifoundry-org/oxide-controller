package cluster

import (
	"context"
	"fmt"
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

	if _, err := ensureImagesExist(ctx, c.logger, c.client, c.projectID, c.controlPlaneImage, c.workerImage); err != nil {
		return nil, fmt.Errorf("image verification failed: %v", err)
	}

	return c.ensureClusterExists(ctx, timeoutMinutes)
}
