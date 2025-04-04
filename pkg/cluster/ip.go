package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

func GetControlPlaneIP(ctx context.Context, logger *log.Entry, client *oxide.Client, projectID, controlPlanePrefix string) (*oxide.FloatingIp, error) {
	var controlPlaneIP *oxide.FloatingIp
	// TODO: Do we need pagination? Using arbitrary limit for now.
	logger.Debugf("Listing floating IPs for project %s", projectID)
	fips, err := client.FloatingIpList(ctx, oxide.FloatingIpListParams{
		Project: oxide.NameOrId(projectID),
		Limit:   oxide.NewPointer(32),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list floating IPs: %w", err)
	}
	logger.Debugf("Found %d floating IPs", len(fips.Items))
	for _, fip := range fips.Items {
		logger.Tracef("trying FIP %s", fip.Name)
		if strings.HasPrefix(string(fip.Name), controlPlanePrefix) {
			controlPlaneIP = &fip
			logger.Debugf("Found control plane floating IP: %s", controlPlaneIP)
			break
		}
	}
	// controlPlaneIP might be nil, if none found
	return controlPlaneIP, nil
}

func (c *Cluster) ensureControlPlaneIP(ctx context.Context, controlPlanePrefix string) (*oxide.FloatingIp, error) {
	var controlPlaneIP *oxide.FloatingIp
	c.logger.Debugf("getting control plane IP for prefix %s", controlPlanePrefix)
	controlPlaneIP, err := GetControlPlaneIP(ctx, c.logger, c.client, c.projectID, controlPlanePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get control plane IP: %w", err)
	}
	// what if we did not find one?
	if controlPlaneIP == nil {
		c.logger.Infof("Control plane floating IP not found. Creating one...")
		fip, err := c.client.FloatingIpCreate(ctx, oxide.FloatingIpCreateParams{
			Project: oxide.NameOrId(c.projectID),
			Body: &oxide.FloatingIpCreate{
				Name:        oxide.Name(fmt.Sprintf("%s-floating-ip", controlPlanePrefix)),
				Description: fmt.Sprintf("Floating IP for Kubernetes control plane nodes with prefix '%s'", controlPlanePrefix),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create floating IP: %w", err)
		}
		controlPlaneIP = fip
		c.logger.Infof("Created floating IP: %s", controlPlaneIP.Ip)
	}
	return controlPlaneIP, nil
}

func attachIP(floatingIp *oxide.FloatingIp) {
	externalIps := []oxide.ExternalIpCreate{}
	if floatingIp != nil {
		externalIps = append(externalIps, oxide.ExternalIpCreate{
			Type:       oxide.ExternalIpCreateTypeFloating,
			FloatingIp: oxide.NameOrId(floatingIp.Id),
		})
	}

}
