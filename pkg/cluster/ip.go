package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

func GetControlPlaneIP(ctx context.Context, client *oxide.Client, projectID, controlPlanePrefix string) (*oxide.FloatingIp, error) {
	var controlPlaneIP *oxide.FloatingIp
	// TODO: Do we need pagination? Using arbitrary limit for now.
	fips, err := client.FloatingIpList(ctx, oxide.FloatingIpListParams{
		Project: oxide.NameOrId(projectID),
		Limit:   oxide.NewPointer(32),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list floating IPs: %w", err)
	}
	for _, fip := range fips.Items {
		if strings.HasPrefix(string(fip.Name), controlPlanePrefix) {
			controlPlaneIP = &fip
			break
		}
	}
	// controlPlaneIP might be nil, if none found
	return controlPlaneIP, nil
}

func ensureControlPlaneIP(ctx context.Context, client *oxide.Client, projectID, controlPlanePrefix string) (*oxide.FloatingIp, error) {
	var controlPlaneIP *oxide.FloatingIp
	controlPlaneIP, err := GetControlPlaneIP(ctx, client, projectID, controlPlanePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get control plane IP: %w", err)
	}
	// what if we did not find one?
	if controlPlaneIP == nil {
		log.Printf("Control plane floating IP not found. Creating one...")
		fip, err := client.FloatingIpCreate(ctx, oxide.FloatingIpCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.FloatingIpCreate{
				Name:        oxide.Name(fmt.Sprintf("%s-floating-ip", controlPlanePrefix)),
				Description: fmt.Sprintf("Floating IP for Kubernetes control plane nodes with prefix '%s'", controlPlanePrefix),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create floating IP: %w", err)
		}
		controlPlaneIP = fip
		log.Printf("Created floating IP: %s", controlPlaneIP.Ip)
	}
	return controlPlaneIP, nil
}
