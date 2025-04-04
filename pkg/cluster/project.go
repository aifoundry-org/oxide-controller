package cluster

import (
	"context"
	"fmt"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

// ensureProjectExists checks if the right project exists and returns its ID
func (c *Cluster) ensureProjectExists(ctx context.Context) (string, error) {
	// TODO: We don't need to list Projects to find specific one, we can `View`
	//       it by name.
	// TODO: do we need pagination? Using arbitrary limit for now.
	projects, err := c.client.ProjectList(ctx, oxide.ProjectListParams{Limit: oxide.NewPointer(32)})
	if err != nil {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}

	var projectID string
	for _, project := range projects.Items {
		if string(project.Name) == c.projectID {
			log.Printf("Cluster project '%s' exists.", c.projectID)
			projectID = project.Id
			break
		}
	}

	if projectID == "" {
		log.Printf("Cluster project '%s' does not exist. Creating it...", c.projectID)
		newProject, err := c.client.ProjectCreate(ctx, oxide.ProjectCreateParams{
			Body: &oxide.ProjectCreate{Name: oxide.Name(c.projectID)},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create project: %w", err)
		}
		projectID = newProject.Id
		log.Printf("Created project '%s' with ID '%s'", c.projectID, projectID)
	}
	return projectID, nil
}
