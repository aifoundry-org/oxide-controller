package cluster

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/oxidecomputer/oxide.go/oxide"
)

func CreateInstance(ctx context.Context, client *oxide.Client, projectID, instanceName string, spec NodeSpec, cloudConfig string) (*oxide.Instance, error) {
	return client.InstanceCreate(ctx, oxide.InstanceCreateParams{
		Project: oxide.NameOrId(projectID),
		Body: &oxide.InstanceCreate{
			Name:        oxide.Name(instanceName),
			Description: instanceName,
			Hostname:    oxide.Hostname(instanceName),
			Memory:      oxide.ByteCount(spec.MemoryGB * GB),
			Ncpus:       oxide.InstanceCpuCount(spec.CPUCount),
			BootDisk: &oxide.InstanceDiskAttachment{
				Type: "create",
				DiskSource: oxide.DiskSource{
					Type:      oxide.DiskSourceTypeImage,
					ImageId:   spec.Image.ID,
					BlockSize: blockSize,
				},
				Size:        oxide.ByteCount(spec.DiskSize),
				Name:        oxide.Name(instanceName),
				Description: instanceName,
			},
			ExternalIps: []oxide.ExternalIpCreate{
				{
					Type: oxide.ExternalIpCreateTypeEphemeral,
				},
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
	var (
		initFlag, tokenFlag, sanFlag, typeFlag, serverFlag, nodeFlag string
		port                                                         int = 6443
	)

	switch nodeType {
	case "server":
		typeFlag = "server"
		if initCluster {
			initFlag = "--cluster-init"
			sanFlag = fmt.Sprintf("--tls-san %s", controlPlaneIP)
			nodeFlag = fmt.Sprintf("--node-external-ip %s", controlPlaneIP)
		} else {
			serverFlag = fmt.Sprintf("--server https://%s:%d", controlPlaneIP, port)
			tokenFlag = fmt.Sprintf("--token %s", joinToken)
		}
	case "agent":
		typeFlag = "agent"
		serverFlag = fmt.Sprintf("--server https://%s:%d", controlPlaneIP, port)
		tokenFlag = fmt.Sprintf("--token %s", joinToken)
	default:
		return "", fmt.Errorf("unknown node type: %s", nodeType)
	}
	var sshKeysSection string
	if len(pubkey) > 0 {
		sshKeysSection = "    ssh-authorized-keys:\n"
		for _, key := range pubkey {
			sshKeysSection += fmt.Sprintf("      - %s\n", key)
		}
	}

	content := fmt.Sprintf(`
#cloud-config
users:
  - name: root
%s  
    shell: /bin/bash
ssh_pwauth: false  # disables password logins
disable_root: false  # ensure root isn't disabled
allow_public_ssh_keys: true
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - %s %s %s %s %s %s --tls-san ${PRIVATE_IP} --tls-san ${PUBLIC_IP}
`, sshKeysSection, typeFlag, initFlag, sanFlag, tokenFlag, serverFlag, nodeFlag)
	return base64.StdEncoding.EncodeToString([]byte(content)), nil
}

// createControlPlaneNodes creates new control plane nodes
func (c *Cluster) CreateControlPlaneNodes(ctx context.Context, initCluster bool, count, start int, additionalPubKeys []string) ([]oxide.Instance, error) {
	var controlPlaneNodes []oxide.Instance
	c.logger.Debugf("Creating %d control plane nodes with prefix %s", count, c.prefix)

	var joinToken string
	var pubkey []byte
	var err error

	if !initCluster {
		joinToken, err = c.GetJoinToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get join token: %w", err)
		}
		pubkey, err = c.GetUserSSHPublicKey(ctx)
		if err != nil && !errors.Is(err, &SecretKeyNotFoundError{}) {
			return nil, fmt.Errorf("failed to get user SSH public key: %w", err)
		}
	}

	pubKeyList := []string{}

	if len(pubkey) > 0 {
		pubKeyList = append(pubKeyList, string(pubkey))
	}
	if additionalPubKeys != nil {
		pubKeyList = append(pubKeyList, additionalPubKeys...)
	}
	cloudConfig, err := GenerateCloudConfig("server", initCluster, c.controlPlaneIP, joinToken, pubKeyList)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cloud config: %w", err)
	}
	for i := start; i < count; i++ {
		instance, err := CreateInstance(ctx, c.client, c.projectID, fmt.Sprintf("%s%d", c.prefix, i), c.controlPlaneSpec, cloudConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create control plane node: %w", err)
		}
		controlPlaneNodes = append(controlPlaneNodes, *instance)
	}
	c.logger.Debugf("Created %d control plane nodes with prefix %s", count, c.prefix)
	return controlPlaneNodes, nil
}

// CreateWorkerNodes creates new worker nodes
func (c *Cluster) CreateWorkerNodes(ctx context.Context, count int) ([]oxide.Instance, error) {
	var nodes []oxide.Instance
	c.logger.Debugf("Creating %d worker nodes with prefix %s", count, c.prefix)
	joinToken, err := c.GetJoinToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get join token: %w", err)
	}
	pubkey, err := c.GetUserSSHPublicKey(ctx)
	if err != nil && !errors.Is(err, &SecretKeyNotFoundError{}) {
		return nil, fmt.Errorf("failed to get user SSH public key: %w", err)
	}
	var pubkeys []string
	if len(pubkey) > 0 {
		pubkeys = append(pubkeys, string(pubkey))
	}
	cloudConfig, err := GenerateCloudConfig("agent", false, c.controlPlaneIP, joinToken, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cloud config: %w", err)
	}

	for i := 0; i < count; i++ {
		workerName := fmt.Sprintf("worker-%d", time.Now().Unix())
		instance, err := CreateInstance(ctx, c.client, c.projectID, workerName, c.workerSpec, cloudConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create worker node: %w", err)
		}
		nodes = append(nodes, *instance)
	}
	c.logger.Debugf("Created %d control plane nodes with prefix %s", count, c.prefix)
	return nodes, nil
}
