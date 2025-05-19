package cluster

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aifoundry-org/oxide-controller/pkg/util"
	"github.com/google/uuid"
	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	extraDisk  = "/dev/nvme1n1"
	extraMount = "/data"
)

func CreateInstance(ctx context.Context, client *oxide.Client, projectID, instanceName string, spec NodeSpec, cloudConfig string) (*oxide.Instance, error) {
	// every disk needs a unique name. Unfortunately, due to a bug in how
	// the disks are provided a controller, if the first 20 characters are the same
	// then you end up with controller conflicts.
	// Until the bug is fixed, we will just add a random 8-character prefix to the name.
	var disks []oxide.InstanceDiskAttachment
	if spec.ExtraDiskSize > 0 {
		prefix, err := util.RandomString(8, true)
		if err != nil {
			return nil, fmt.Errorf("failed to generate random string: %w", err)
		}
		disks = append(disks, oxide.InstanceDiskAttachment{
			Type: oxide.InstanceDiskAttachmentTypeCreate,
			DiskSource: oxide.DiskSource{
				Type:      oxide.DiskSourceTypeBlank,
				BlockSize: blockSize,
			},
			Size:        oxide.ByteCount(spec.ExtraDiskSize),
			Name:        oxide.Name(fmt.Sprintf("d-%s-%s-disk-1", prefix, instanceName)),
			Description: instanceName,
		})
	}
	prefix, err := util.RandomString(8, true)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %w", err)
	}
	hostnameSuffix, err := util.RandomString(8, true)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %w", err)
	}
	createBody := &oxide.InstanceCreate{
		Name:        oxide.Name(instanceName),
		Description: instanceName,
		Hostname:    oxide.Hostname(fmt.Sprintf("%s-%s", instanceName, hostnameSuffix)),
		Memory:      oxide.ByteCount(spec.MemoryGB * GB),
		Ncpus:       oxide.InstanceCpuCount(spec.CPUCount),
		NetworkInterfaces: oxide.InstanceNetworkInterfaceAttachment{
			Type: "default",
		},
		BootDisk: &oxide.InstanceDiskAttachment{
			Type: oxide.InstanceDiskAttachmentTypeCreate,
			DiskSource: oxide.DiskSource{
				Type:      oxide.DiskSourceTypeImage,
				ImageId:   spec.Image.ID,
				BlockSize: blockSize,
			},
			Size:        oxide.ByteCount(spec.RootDiskSize),
			Name:        oxide.Name(fmt.Sprintf("d-%s-%s", prefix, instanceName)),
			Description: instanceName,
		},

		UserData: cloudConfig,
		Disks:    disks,
	}
	params := oxide.InstanceCreateParams{
		Project: oxide.NameOrId(projectID),
		Body:    createBody,
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
func GenerateCloudConfigB64(nodeType string, initCluster bool, controlPlaneIP, joinToken string, pubkey []string, extraDisk, tailscaleAuthKey, tailscaleTag string) (string, error) {
	cloudConfig, err := GenerateCloudConfig(nodeType, initCluster, controlPlaneIP, joinToken, pubkey, extraDisk, tailscaleAuthKey, tailscaleTag)
	if err != nil {
		return "", fmt.Errorf("failed to generate cloud config: %w", err)
	}
	return base64.StdEncoding.EncodeToString(cloudConfig), nil
}

// GenerateCloudConfig for a particular node type
func GenerateCloudConfig(nodeType string, initCluster bool, controlPlaneIP, joinToken string, pubkey []string, extraDisk, tailscaleAuthKey, tailscaleTag string) ([]byte, error) {
	var (
		k3sArgs []string
		port    int = 6443
	)

	cfg := CloudConfig{}

	// SSH access
	cfg.AllowPublicSSHKeys = true
	cfg.SSHPWAuth = false
	cfg.DisableRoot = false
	if len(pubkey) > 0 {
		cfg.Users = []User{
			{Name: "root", Shell: "/bin/bash", SSHAuthorizedKeys: pubkey},
		}
	}

	// tailscale
	// if provided, add it to the config; this MUST come before k3s so that the IP is added
	if tailscaleAuthKey != "" {
		cfg.RunCmd = append(cfg.RunCmd, []string{
			"curl -fsSL https://pkgs.tailscale.com/stable/debian/bullseye.noarmor.gpg | sudo tee /usr/share/keyrings/tailscale-archive-keyring.gpg >/dev/null",
			"curl -fsSL https://pkgs.tailscale.com/stable/debian/bullseye.tailscale-keyring.list | sudo tee /etc/apt/sources.list.d/tailscale.list",
			"sudo apt-get update -y",
			"sudo apt-get install -y tailscale",
			"systemctl enable tailscaled",
			"systemctl start tailscaled",
			fmt.Sprintf("tailscale up --advertise-tags=tag:%s --authkey %s", tailscaleTag, tailscaleAuthKey),
			"TAILSCALE_IP=$(tailscale ip --4)",
		})
	}

	// if extra disk provided, add it to the config
	if extraDisk != "" {
		// create a UUID for the filesystem
		uid := uuid.New()
		cfg.RunCmd = append(cfg.RunCmd, []string{
			fmt.Sprintf("mkdir -p %s", extraMount),
			// attempt to mount, only format if mount fails
			fmt.Sprintf("mount %s %s || (mkfs.ext4 -U %s %s && mount %s %s)", extraDisk, extraMount, uid, extraDisk, extraDisk, extraMount),
			fmt.Sprintf("echo 'UUID=%s %s ext4 defaults 0 0' | tee -a /etc/fstab", uid, extraMount),
		})
	}

	// k3s
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
		// add the tailscale IP if provided
		if tailscaleAuthKey != "" {
			k3sArgs = append(k3sArgs, "--tls-san ${TAILSCALE_IP}")
		}
	case "agent":
		k3sArgs = append(k3sArgs, "agent")
		k3sArgs = append(k3sArgs, fmt.Sprintf("--server https://%s:%d", controlPlaneIP, port))
		k3sArgs = append(k3sArgs, fmt.Sprintf("--token %s", joinToken))
	default:
		return nil, fmt.Errorf("unknown node type: %s", nodeType)
	}
	cfg.RunCmd = append(cfg.RunCmd, []string{
		"PRIVATE_IP=$(hostname -I | awk '{print $1}')",
		"PUBLIC_IP=$(curl -s https://ifconfig.me)",
		fmt.Sprintf("curl -sfL https://get.k3s.io | sh -s - %s", strings.Join(k3sArgs, " ")),
	})

	// encode
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
	client, err := oxide.NewClient(c.oxideConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Oxide API client: %v", err)
	}
	var controlPlaneNodes []oxide.Instance
	c.logger.Debugf("Creating %d control plane nodes with prefix %s", count, c.controlPlanePrefix)

	var joinToken string
	var pubkey []byte

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
	var extraNodeDisk string
	if c.controlPlaneSpec.ExtraDiskSize > 0 {
		extraNodeDisk = extraDisk
	}
	cloudConfig, err := GenerateCloudConfigB64("server", initCluster, c.controlPlaneIP, joinToken, pubKeyList, extraNodeDisk, c.controlPlaneSpec.TailscaleAuthKey, c.controlPlaneSpec.TailscaleTag)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cloud config: %w", err)
	}

	for i := start; i < start+count; i++ {
		instance, err := CreateInstance(ctx, client, c.projectID, fmt.Sprintf("%s%d", c.controlPlanePrefix, i), c.controlPlaneSpec, cloudConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create control plane node: %w", err)
		}
		controlPlaneNodes = append(controlPlaneNodes, *instance)
	}
	c.logger.Debugf("Created %d control plane nodes with prefix %s", count, c.controlPlanePrefix)
	return controlPlaneNodes, nil
}

// EnsureWorkerNodes ensures the count of worker nodes matches what it should be
func (c *Cluster) EnsureWorkerNodes(ctx context.Context) ([]oxide.Instance, error) {
	client, err := oxide.NewClient(c.oxideConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Oxide API client: %v", err)
	}
	// try to get the worker count from the cluster
	// if it fails, we will use the default value
	// if it succeeds, we will use that value
	count, err := c.GetWorkerCount(ctx)
	if err != nil {
		if !errors.Is(err, &SecretKeyNotFoundError{}) {
			return nil, fmt.Errorf("failed to get worker count: %w", err)
		}
		c.logger.Debugf("Failed to get worker count from cluster, using CLI flag value and storing")
		count = c.workerCount
		if err := c.SetWorkerCount(ctx, count); err != nil {
			return nil, fmt.Errorf("failed to set worker count: %w", err)
		}
		c.logger.Debugf("Set worker count to %d", count)
	}
	c.logger.Debugf("Ensuring %d worker nodes", count)
	var nodes []oxide.Instance
	// first check how many worker nodes we have, by asking the cluster
	_, workers, err := getNodesOxide(ctx, c.logger, client, c.projectID, c.controlPlanePrefix, c.workerPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}
	actualCount := len(workers)
	c.logger.Debugf("Found %d worker nodes, desired %d", actualCount, count)
	if actualCount >= int(count) {
		c.logger.Debugf("Already have enough worker nodes, not creating any")
		return nil, nil
	}
	c.logger.Debugf("Need to create %d more worker nodes", count-actualCount)
	// we had less than we wanted, so create more
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
	var extraNodeDisk string
	if c.workerSpec.ExtraDiskSize > 0 {
		extraNodeDisk = extraDisk
	}
	cloudConfig, err := GenerateCloudConfigB64("agent", false, c.controlPlaneIP, joinToken, pubkeys, extraNodeDisk, c.workerSpec.TailscaleAuthKey, c.workerSpec.TailscaleTag)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cloud config: %w", err)
	}

	for i := actualCount; i < int(count); i++ {
		workerName := fmt.Sprintf("%s%d", c.workerPrefix, time.Now().Unix())
		instance, err := CreateInstance(ctx, client, c.projectID, workerName, c.workerSpec, cloudConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create worker node: %w", err)
		}
		nodes = append(nodes, *instance)
	}
	c.logger.Debugf("Created %d worker nodes with prefix %s", count, c.workerPrefix)
	return nodes, nil
}

// GetWorkerNodeCount gets the number of worker nodes in the cluster
func (c *Cluster) GetWorkerNodeCount() (int, error) {
	return c.GetWorkerCount(context.TODO())
}

// getNodesKubernetes gets the node names from the cluster
// Returns a list of node names, first control plane and then worker nodes
func getNodesKubernetes(ctx context.Context, logger *log.Entry, config *rest.Config) ([]string, []string, error) {
	logger.Debugf("Getting nodes from Kubernetes")

	clientset, err := getClientset(config)
	if err != nil {
		return nil, nil, err
	}

	// Get the nodes
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}

	var controlPlaneNodes, workerNodes []string
	for _, node := range nodes.Items {
		labels := node.Labels

		_, isControlPlane := labels["node-role.kubernetes.io/control-plane"]
		_, isMaster := labels["node-role.kubernetes.io/master"]

		if isControlPlane || isMaster {
			controlPlaneNodes = append(controlPlaneNodes, node.Name)
		} else {
			workerNodes = append(workerNodes, node.Name)
		}
	}
	return controlPlaneNodes, workerNodes, nil
}

// getNodesOxide gets the node names from Oxide, first worker then control plane nodes
func getNodesOxide(ctx context.Context, logger *log.Entry, client *oxide.Client, projectID, controlPlanePrefix, workerPrefix string) ([]string, []string, error) {
	logger.Debugf("Getting nodes from Oxide with project ID %s", projectID)
	// TODO: endpoint is paginated, using arbitrary limit for now.
	instances, err := client.InstanceList(ctx, oxide.InstanceListParams{
		Project: oxide.NameOrId(projectID),
		Limit:   oxide.NewPointer(32),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list instances: %w", err)
	}

	var controlPlaneNodes, workerNodes []string
	for _, instance := range instances.Items {
		name := string(instance.Name)
		switch {
		case strings.HasPrefix(name, controlPlanePrefix):
			controlPlaneNodes = append(controlPlaneNodes, name)
		case strings.HasPrefix(name, workerPrefix):
			workerNodes = append(workerNodes, name)
		}
	}
	return controlPlaneNodes, workerNodes, nil
}
