package cluster

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aifoundry-org/oxide-controller/pkg/util"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

type Cluster struct {
	logger                       *log.Entry
	client                       *oxide.Client
	projectID                    string
	prefix                       string
	controlPlaneCount            atomic.Uint32
	workerCount                  atomic.Uint32
	controlPlaneSpec, workerSpec NodeSpec
	secretName                   string
	kubeconfig, userPubkey       []byte
	controlPlaneIP               string
}

// New creates a new Cluster instance
func New(logger *log.Entry, client *oxide.Client, projectID string, prefix string, controlPlaneCount, workerCount uint32, controlPlaneSpec, workerSpec NodeSpec, secretName string, kubeconfig, pubkey []byte) *Cluster {
	c := &Cluster{
		logger:           logger.WithField("component", "cluster"),
		client:           client,
		projectID:        projectID,
		prefix:           prefix,
		controlPlaneSpec: controlPlaneSpec,
		workerSpec:       workerSpec,
		secretName:       secretName,
		kubeconfig:       kubeconfig,
		userPubkey:       pubkey,
	}
	c.workerCount.Store(workerCount)
	c.controlPlaneCount.Store(controlPlaneCount)
	return c
}

// ensureClusterExists checks if a k3s cluster exists, and creates one if needed
func (c *Cluster) ensureClusterExists(ctx context.Context, timeoutMinutes int, existingKubeconfig []byte, kubeconfigOverwrite bool) (newKubeconfig []byte, err error) {
	// local vars just for convenience
	client := c.client
	projectID := c.projectID
	controlPlanePrefix := c.prefix
	controlPlaneCount := c.controlPlaneCount.Load()
	secretName := c.secretName

	c.logger.Debugf("Checking if %d control plane nodes exist with prefix %s", controlPlaneCount, controlPlanePrefix)

	// TODO: endpoint is paginated, using arbitrary limit for now.
	instances, err := client.InstanceList(ctx, oxide.InstanceListParams{
		Project: oxide.NameOrId(projectID),
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
	if len(controlPlaneNodes) >= int(controlPlaneCount) {
		return nil, nil
	}

	if len(controlPlaneNodes) > 0 && len(existingKubeconfig) > 0 {
		c.logger.Debugf("Found %d control plane nodes, but kubeconfig already exists", len(controlPlaneNodes))
		// TODO: check to see if it can access the cluster
	}

	if len(controlPlaneNodes) == 0 && len(existingKubeconfig) > 0 && !kubeconfigOverwrite {
		c.logger.Debugf("Found no control plane nodes, but kubeconfig already exists")
		return nil, fmt.Errorf("kubeconfig already exists but cluster does not")
	}

	controlPlaneIP, err := c.ensureControlPlaneIP(ctx, controlPlanePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get control plane IP: %w", err)
	}

	if c.controlPlaneIP == "" {
		c.controlPlaneIP = controlPlaneIP.Ip
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

	var kubeconfig = c.kubeconfig
	// if we did not have any nodes, create a cluster
	if len(controlPlaneNodes) == 0 {
		highest++
		secrets := make(map[string][]byte)
		priv, pub, err := util.SSHKeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate SSH key pair: %w", err)
		}
		var pubkeyList []string
		if c.userPubkey != nil {
			pubkeyList = append(pubkeyList, string(c.userPubkey))
		}
		pubkeyList = append(pubkeyList, string(pub))
		// add the public key to the node in addition to the user one
		instances, err := c.CreateControlPlaneNodes(ctx, true, 1, highest, pubkeyList)
		if err != nil {
			return nil, fmt.Errorf("failed to create control plane node: %w", err)
		}
		if len(instances) < 1 {
			return nil, fmt.Errorf("created 0 control plane nodes")
		}
		hostid := instances[0].Id
		// if the control plane node was not configured to have an external IP,
		// attach the floating IP to start and use that is the externalIP
		var (
			externalIP  string
			fipAttached bool
		)
		if c.controlPlaneSpec.ExternalIP {
			log.Debugf("Control plane node %s has external IP, using that", hostid)
			ipList, err := client.InstanceExternalIpList(ctx, oxide.InstanceExternalIpListParams{
				Instance: oxide.NameOrId(hostid),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to get external IP list: %w", err)
			}
			if len(ipList.Items) < 1 {
				return nil, fmt.Errorf("created control plane node has no external IP")
			}
			externalIP = ipList.Items[0].Ip
		} else {
			// attach the floating IP to the control plane node
			log.Debugf("Control plane node %s does not have external IP, attaching and using floating IP", hostid)
			// floating ip attachment sometimes just doesn't work right after we create the node,
			// so give it a few retries
			var (
				maxTries          = 5
				sleepBetweenTries = 3 * time.Second
				attached          bool
			)
			for i := 0; i < maxTries; i++ {
				time.Sleep(sleepBetweenTries)
				if _, err = client.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
					FloatingIp: oxide.NameOrId(controlPlaneIP.Id),
					Body: &oxide.FloatingIpAttach{
						Kind:   oxide.FloatingIpParentKindInstance,
						Parent: oxide.NameOrId(hostid),
					},
				}); err != nil {
					log.Debugf("Failed to attach floating IP %v, retrying %d/%d", err, i+1, maxTries)
					continue
				}
				log.Debug("Successfully attached floating IP")
				attached = true
				break
			}
			if !attached {
				return nil, fmt.Errorf("failed to attach floating IP after %d tries, last error: %w", maxTries, err)
			}
			externalIP = controlPlaneIP.Ip
			fipAttached = true
		}

		// wait for the control plane node to be up and running
		timeLeft := time.Duration(timeoutMinutes) * time.Minute
		for {
			c.logger.Infof("Waiting %s for control plane node %s to be up and running...", timeLeft, controlPlaneIP)
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
		// attach the floating IP to the control plane node, if not done already
		if !fipAttached {
			log.Debugf("Control plane node %s did not have floating IP attached, attaching", hostid)
			if _, err := client.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
				FloatingIp: oxide.NameOrId(controlPlaneIP.Id),
				Body: &oxide.FloatingIpAttach{
					Kind:   oxide.FloatingIpParentKindInstance,
					Parent: oxide.NameOrId(hostid),
				},
			}); err != nil {
				return nil, fmt.Errorf("failed to attach floating IP: %w", err)
			}
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
		if c.userPubkey != nil {
			secrets[secretKeyUserSSH] = c.userPubkey
		}

		// get the kubeconfig
		kubeconfig, err = util.RunSSHCommand("root", fmt.Sprintf("%s:22", externalIP), priv, "cat /etc/rancher/k3s/k3s.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to run command to retrieve kubeconfig on control plane node: %w", err)
		}

		c.logger.Debugf("retrieved new kubeconfig of size %d", len(kubeconfig))

		// have to change the kubeconfig to use the floating IP
		kubeconfigString := string(kubeconfig)
		re := regexp.MustCompile(`(server:\s*\w+://)(\d+\.\d+\.\d+\.\d+)(:\d+)`)
		kubeconfigString = re.ReplaceAllString(kubeconfigString, fmt.Sprintf("${1}%s${3}", controlPlaneIP.Ip))
		c.kubeconfig = []byte(kubeconfigString)

		// save the join token, system ssh key pair, user ssh key to the Kubernetes secret
		c.logger.Debugf("Saving secret %s to Kubernetes", secretName)
		if err := saveSecret(ctx, c.logger, secretName, c.kubeconfig, secrets); err != nil {
			return nil, fmt.Errorf("failed to save secret: %w", err)
		}

		controlPlaneNodes = append(controlPlaneNodes, instances[0])
	}

	// the number we want is the next one
	highest++
	count := int(controlPlaneCount) - len(controlPlaneNodes)
	c.logger.Debugf("control plane nodes %d, desired %d, creating %d", len(controlPlaneNodes), controlPlaneCount, count)

	if _, err := c.CreateControlPlaneNodes(ctx, false, count, highest, nil); err != nil {
		return nil, fmt.Errorf("failed to create control plane node: %w", err)
	}

	c.logger.Debugf("Completed %d control plane nodes exist with prefix %s", controlPlaneCount, controlPlanePrefix)
	return c.kubeconfig, nil
}
