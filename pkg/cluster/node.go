package cluster

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oxidecomputer/oxide.go/oxide"
	"gopkg.in/yaml.v3"
)

func CreateInstance(ctx context.Context, client *oxide.Client, projectID, instanceName string, spec NodeSpec, cloudConfig string) (*oxide.Instance, error) {
	params := oxide.InstanceCreateParams{
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
			NetworkInterfaces: oxide.InstanceNetworkInterfaceAttachment{
				Type: "default",
			},
			UserData: cloudConfig,
		},
	}
	if spec.ExternalIP {
		params.Body.ExternalIps = []oxide.ExternalIpCreate{
			{
				Type: oxide.ExternalIpCreateTypeEphemeral,
			},
		}
	}
	return client.InstanceCreate(ctx, params)
}

// GenerateCloudConfigB64 generates a base64 encoded cloud config for a particular node type
func GenerateCloudConfigB64(nodeType string, initCluster bool, controlPlaneIP, joinToken string, pubkey []string) (string, error) {
	cloudConfig, err := GenerateCloudConfig(nodeType, initCluster, controlPlaneIP, joinToken, pubkey)
	if err != nil {
		return "", fmt.Errorf("failed to generate cloud config: %w", err)
	}
	return base64.StdEncoding.EncodeToString(cloudConfig), nil
}

// GenerateCloudConfig for a particular node type
func GenerateCloudConfig(nodeType string, initCluster bool, controlPlaneIP, joinToken string, pubkey []string) ([]byte, error) {
	var (
		k3sArgs []string
		port    int = 6443
	)

	switch nodeType {
	case "server":
		k3sArgs = append(k3sArgs, "server")
		if initCluster {
			k3sArgs = append(k3sArgs, "--cluster-init")
			k3sArgs = append(k3sArgs, fmt.Sprintf("--tls-san %s", controlPlaneIP))
			k3sArgs = append(k3sArgs, fmt.Sprintf("--node-external-ip %s", controlPlaneIP))
		} else {
			k3sArgs = append(k3sArgs, fmt.Sprintf("--server https://%s:%d", controlPlaneIP, port))
			k3sArgs = append(k3sArgs, fmt.Sprintf("--token %s", joinToken))
		}
		k3sArgs = append(k3sArgs, "--tls-san ${PRIVATE_IP} --tls-san ${PUBLIC_IP}")
	case "agent":
		k3sArgs = append(k3sArgs, "agent")
		k3sArgs = append(k3sArgs, fmt.Sprintf("--server https://%s:%d", controlPlaneIP, port))
		k3sArgs = append(k3sArgs, fmt.Sprintf("--token %s", joinToken))
	default:
		return nil, fmt.Errorf("unknown node type: %s", nodeType)
	}
	cfg := CloudConfig{}
	if len(pubkey) > 0 {
		cfg.Users = []User{
			{Name: "root", Shell: "/bin/bash", SSHAuthorizedKeys: pubkey},
		}
	}
	cfg.RunCmd = MultiLineStrings{
		{
			"PRIVATE_IP=$(hostname -I | awk '{print $1}')",
			"PUBLIC_IP=$(curl -s https://ifconfig.me)",
			fmt.Sprintf("curl -sfL https://get.k3s.io | sh -s - %s", strings.Join(k3sArgs, " ")),
		},
	}
	cfg.AllowPublicSSHKeys = true
	cfg.SSHPWAuth = false
	cfg.DisableRoot = false
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	defer enc.Close()

	if err := enc.Encode(cfg); err != nil {
		return nil, fmt.Errorf("failed to marshal cloud config: %w", err)
	}

	content := buf.Bytes()
	return append([]byte("#cloud-config\n"), content...), nil
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
	cloudConfig, err := GenerateCloudConfigB64("server", initCluster, c.controlPlaneIP, joinToken, pubKeyList)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cloud config: %w", err)
	}

	for i := start; i < start+count; i++ {
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
	cloudConfig, err := GenerateCloudConfigB64("agent", false, c.controlPlaneIP, joinToken, pubkeys)
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
