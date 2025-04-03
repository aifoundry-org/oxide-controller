package cluster

import (
	"context"
	"fmt"
	"log"

	"github.com/oxidecomputer/oxide.go/oxide"
)

// ensureProjectExists checks if the right project exists and returns its ID
func ensureProjectExists(ctx context.Context, client *oxide.Client, clusterProject string) (string, error) {
	// TODO: We don't need to list Projects to find specific one, we can `View`
	//       it by name.
	// TODO: do we need pagination? Using arbitrary limit for now.
	projects, err := client.ProjectList(ctx, oxide.ProjectListParams{Limit: oxide.NewPointer(32)})
	if err != nil {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}

	var projectID string
	for _, project := range projects.Items {
		if string(project.Name) == clusterProject {
			log.Printf("Cluster project '%s' exists.", clusterProject)
			projectID = project.Id
			break
		}
	}

	if projectID == "" {
		log.Printf("Cluster project '%s' does not exist. Creating it...", clusterProject)
		newProject, err := client.ProjectCreate(ctx, oxide.ProjectCreateParams{
			Body: &oxide.ProjectCreate{Name: oxide.Name(clusterProject)},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create project: %w", err)
		}
		projectID = newProject.Id
		log.Printf("Created project '%s' with ID '%s'", clusterProject, projectID)
	}
	return projectID, nil
}
