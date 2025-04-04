package cluster

import (
	"context"
	"fmt"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

// ensureProjectExists checks if the right project exists and returns its ID
func ensureProjectExists(ctx context.Context, logger *log.Logger, client *oxide.Client, projectName string) (string, error) {
	// TODO: We don't need to list Projects to find specific one, we can `View`
	//       it by name.
	// TODO: do we need pagination? Using arbitrary limit for now.
	logger.Debugf("Listing projects to check if %s exists", projectName)
	projects, err := client.ProjectList(ctx, oxide.ProjectListParams{Limit: oxide.NewPointer(32)})
	if err != nil {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}
	logger.Debugf("Found %d projects", len(projects.Items))

	var projectID string
	for _, project := range projects.Items {
		logger.Tracef("Checking project %s vs desired %s", project.Name, projectName)
		if string(project.Name) == projectName {
			logger.Infof("Cluster project '%s' exists.", projectName)
			projectID = project.Id
			logger.Infof("Using project '%s' with ID '%s'", projectName, projectID)
			break
		}
	}

	if projectID == "" {
		logger.Infof("Cluster project '%s' does not exist. Creating it...", projectName)
		newProject, err := client.ProjectCreate(ctx, oxide.ProjectCreateParams{
			Body: &oxide.ProjectCreate{Name: oxide.Name(projectName)},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create project: %w", err)
		}
		projectID = newProject.Id
		logger.Infof("Created project '%s' with ID '%s'", projectName, projectID)
	}
	return projectID, nil
}
