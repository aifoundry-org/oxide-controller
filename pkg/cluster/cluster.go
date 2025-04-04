package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aifoundry-org/oxide-controller/pkg/util"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

type Cluster struct {
	logger    *log.Logger
	client    *oxide.Client
	projectID string
}

// New creates a new Cluster instance
func New(logger *log.Logger, client *oxide.Client, projectID string) *Cluster {
	return &Cluster{
		logger:    logger,
		client:    client,
		projectID: projectID,
	}
}

// ensureClusterExists checks if a k3s cluster exists, and creates one if needed
func (c *Cluster) ensureClusterExists(ctx context.Context, kubeconfig, userPubKey []byte, controlPlanePrefix string, controlPlaneCount int, controlPlaneImage string, memoryGB, cpuCount int, timeoutMinutes int, secretName string) (newKubeconfig []byte, err error) {
	// TODO: endpoint is paginated, using arbitrary limit for now.
	instances, err := c.client.InstanceList(ctx, oxide.InstanceListParams{
		Project: oxide.NameOrId(c.projectID),
		Limit:   oxide.NewPointer(32),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	var controlPlaneNodes []oxide.Instance
	for _, instance := range instances.Items {
		if strings.HasPrefix(string(instance.Name), controlPlanePrefix) {
			controlPlaneNodes = append(controlPlaneNodes, instance)
		}
	}

	// if we have enough nodes, return
	if len(controlPlaneNodes) >= controlPlaneCount {
		return nil, nil
	}

	controlPlaneIP, err := c.ensureControlPlaneIP(ctx, controlPlanePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get control plane IP: %w", err)
	}

	// find highest number control plane node
	var highest int = -1
	for _, instance := range controlPlaneNodes {
		count := strings.TrimPrefix(instance.Hostname, controlPlanePrefix)
		if count == "" {
			continue
		}
		num, err := strconv.Atoi(count)
		if err != nil {
			continue
		}
		if num > highest {
			highest = num
		}
	}

	// if we did not have any nodes, create a cluster
	if len(controlPlaneNodes) == 0 {
		if len(kubeconfig) > 0 {
			return nil, fmt.Errorf("kubeconfig already exists but cluster does not")
		}
		highest++
		secrets := make(map[string][]byte)
		priv, pub, err := util.SSHKeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate SSH key pair: %w", err)
		}
		var pubkeyList []string
		if userPubKey != nil {
			pubkeyList = append(pubkeyList, string(userPubKey))
		}
		pubkeyList = append(pubkeyList, string(pub))
		// add the public key to the node in addition to the user one
		instances, err := c.createControlPlaneNodes(ctx, true, 1, highest, controlPlaneIP.Ip, "", pubkeyList, controlPlanePrefix, controlPlaneImage, memoryGB, cpuCount)
		if err != nil {
			return nil, fmt.Errorf("failed to create control plane node: %w", err)
		}
		if len(instances) < 1 {
			return nil, fmt.Errorf("created 0 control plane nodes")
		}
		hostid := instances[0].Id
		ipList, err := c.client.InstanceExternalIpList(ctx, oxide.InstanceExternalIpListParams{
			Instance: oxide.NameOrId(hostid),
			Project:  oxide.NameOrId(c.projectID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get external IP list: %w", err)
		}
		if len(ipList.Items) < 1 {
			return nil, fmt.Errorf("created control plane node has no external IP")
		}
		externalIP := ipList.Items[0].Ip
		// wait for the control plane node to be up and running
		timeLeft := time.Duration(timeoutMinutes) * time.Minute
		for {
			c.logger.Infof("Waiting %d mins for control plane node %s to be up and running...", timeLeft, controlPlaneIP)
			sleepTime := 1 * time.Minute
			time.Sleep(sleepTime)
			timeLeft -= sleepTime
			if isClusterAlive(fmt.Sprintf("https://%s:%d", externalIP, 6443)) {
				c.logger.Infof("Control plane at %s is up and running", externalIP)
				break
			}
			if timeLeft <= 0 {
				c.logger.Errorf("Control plane at %s did not respond in time, exiting", externalIP)
				return nil, fmt.Errorf("control plane at %s did not respond in time", externalIP)
			}
		}
		// attach the floating IP to the control plane node
		if _, err := c.client.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
			FloatingIp: oxide.NameOrId(controlPlaneIP.Id),
			Project:    oxide.NameOrId(c.projectID),
			Body: &oxide.FloatingIpAttach{
				Kind:   oxide.FloatingIpParentKindInstance,
				Parent: oxide.NameOrId(hostid),
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to attach floating IP: %w", err)
		}

		// get the join token and save it to our secrets map
		joinToken, err := util.RunSSHCommand("root", fmt.Sprintf("%s:22", externalIP), priv, "cat /var/lib/rancher/k3s/server/node-token")
		if err != nil {
			return nil, fmt.Errorf("failed to run command to retrieve join token on control plane node: %w", err)
		}
		// save the private key and public key to the secret
		secrets[secretKeySystemSSHPublic] = pub
		secrets[secretKeySystemSSHPrivate] = priv
		secrets[secretKeyJoinToken] = joinToken

		// save the user ssh public key to the secrets map
		if userPubKey != nil {
			secrets[secretKeyUserSSH] = userPubKey
		}

		// get the kubeconfig
		kubeconfig, err = util.RunSSHCommand("root", fmt.Sprintf("%s:22", externalIP), priv, "cat /etc/rancher/k3s/k3s.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to run command to retrieve kubeconfig on control plane node: %w", err)
		}

		// save the join token, system ssh key pair, user ssh key to the Kubernetes secret
		if err := saveSecret(secretName, kubeconfig, secrets); err != nil {
			return nil, fmt.Errorf("failed to save secret: %w", err)
		}

		controlPlaneNodes = append(controlPlaneNodes, instances[0])
	}

	// now that we have an initial node, we can get the join token
	joinToken, err := GetJoinToken(ctx, kubeconfig, secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve join token: %v", err)
	}
	// the number we want is the next one
	highest++
	count := controlPlaneCount - len(controlPlaneNodes)
	if _, err := c.createControlPlaneNodes(ctx, false, count, highest, controlPlaneIP.Ip, joinToken, []string{string(userPubKey)}, controlPlanePrefix, controlPlaneImage, memoryGB, cpuCount); err != nil {
		return nil, fmt.Errorf("failed to create control plane node: %w", err)
	}

	return kubeconfig, nil
}
