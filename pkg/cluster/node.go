package cluster

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/oxidecomputer/oxide.go/oxide"
)

func CreateInstance(ctx context.Context, client *oxide.Client, projectID, name, image string, memoryGB, cpuCount int, cloudConfig string, floatingIp *oxide.FloatingIp) (*oxide.Instance, error) {
	externalIps := []oxide.ExternalIpCreate{}
	if floatingIp != nil {
		externalIps = append(externalIps, oxide.ExternalIpCreate{
			Type:       oxide.ExternalIpCreateTypeFloating,
			FloatingIp: oxide.NameOrId(floatingIp.Id),
		})
	}
	return client.InstanceCreate(ctx, oxide.InstanceCreateParams{
		Project: oxide.NameOrId(projectID),
		Body: &oxide.InstanceCreate{
			Name:        oxide.Name(name),
			Description: name,
			Hostname:    oxide.Hostname(name),
			Memory:      oxide.ByteCount(memoryGB * GB),
			Ncpus:       oxide.InstanceCpuCount(cpuCount),
			ExternalIps: externalIps,
			BootDisk: &oxide.InstanceDiskAttachment{
				Type: "create",
				DiskSource: oxide.DiskSource{
					Type:      oxide.DiskSourceTypeImage,
					ImageId:   image,
					BlockSize: blockSize,
				},
				Size:        oxide.ByteCount(3 * GB), // FIXME, use explicit disk size
				Name:        oxide.Name(name),
				Description: name,
			},
			NetworkInterfaces: oxide.InstanceNetworkInterfaceAttachment{
				Type: "default",
			},
			UserData: cloudConfig,
		},
	})
}

// GenerateCloudConfig for a particular node type
func GenerateCloudConfig(nodeType string, initCluster bool, controlPlaneIP, joinToken string, pubkey []string) (string, error) {
	// initial: curl -sfL https://get.k3s.io | sh -s - server --cluster-init --tls-san <floatingIP>
	// control plane nodes: curl -sfL https://get.k3s.io | sh -s - server --server https://${SERVER} --token '${TOKEN}'
	// worker nodes: curl -sfL https://get.k3s.io | sh -s - agent --server https://${SERVER} --token '${TOKEN}'
	var initFlag, tokenFlag, typeFlag, sanFlag, serverFlag string
	switch nodeType {
	case "server":
		typeFlag = "server"
		if initCluster {
			initFlag = "--cluster-init"
			sanFlag = fmt.Sprintf("--tls-san %s", controlPlaneIP)
		} else {
			serverFlag = fmt.Sprintf("--server https://%s:6443", controlPlaneIP)
			tokenFlag = fmt.Sprintf("--token %s", joinToken)
		}
	case "agent":
		typeFlag = "agent"
		serverFlag = fmt.Sprintf("--server https://%s:6443", controlPlaneIP)
		tokenFlag = fmt.Sprintf("--token %s", joinToken)
	default:
		return "", fmt.Errorf("Unknown node type: %s", nodeType)
	}
	content := fmt.Sprintf(`
#cloud-config
users:
  - name: root
    ssh-authorized-keys: [%s]
    shell: /bin/bash
ssh_pwauth: false  # disables password logins
disable_root: false  # ensure root isn't disabled
allow_public_ssh_keys: true
runcmd:
  - curl -sfL https://get.k3s.io | sh -s - %s %s %s %s %s %s
`,
		pubkey,
		typeFlag, initFlag, sanFlag, tokenFlag, serverFlag, sanFlag)
	return base64.StdEncoding.EncodeToString([]byte(content)), nil
}

// createControlPlaneNodes creates new control plane nodes
func (c *Cluster) createControlPlaneNodes(ctx context.Context, initCluster bool, count, start int, controlPlaneIP *oxide.FloatingIp, joinToken string, pubkey []string, prefix string, image string, memoryGB, cpuCount int) ([]oxide.Instance, error) {
	var controlPlaneNodes []oxide.Instance
	c.logger.Debugf("Creating %d control plane nodes with prefix %s", count, prefix)
	cloudConfig, err := GenerateCloudConfig("server", initCluster, controlPlaneIP.Ip, joinToken, pubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cloud config: %w", err)
	}
	for i := start; i < count; i++ {
		instance, err := CreateInstance(ctx, c.client, c.projectID, fmt.Sprintf("%s%d", prefix, i), image, memoryGB, cpuCount, cloudConfig, controlPlaneIP)
		if err != nil {
			return nil, fmt.Errorf("failed to create control plane node: %w", err)
		}
		controlPlaneNodes = append(controlPlaneNodes, *instance)
	}
	c.logger.Debugf("Created %d control plane nodes with prefix %s", count, prefix)
	return controlPlaneNodes, nil
}
