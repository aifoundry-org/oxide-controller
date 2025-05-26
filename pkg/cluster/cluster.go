package cluster

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aifoundry-org/oxide-controller/pkg/config"
	"github.com/aifoundry-org/oxide-controller/pkg/util"
	"k8s.io/client-go/kubernetes"
	"tailscale.com/client/tailscale/v2"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

type Cluster struct {
	// logger
	logger *log.Entry

	// reusable config that should be loaded into the secret and shared, whether running locally or in-cluster
	config *config.ControllerConfig

	// config that is derived locally
	kubeconfigOverwrite bool
	ociImage            string // OCI image to use for the controller
	oxideConfig         *oxide.Config
	clientset           *kubernetes.Clientset
	apiConfig           *Config
	projectID           string        // ID of the Oxide project
	initWait            time.Duration // time to wait for the cluster to initialize
}

// New creates a new Cluster instance
func New(logger *log.Entry, ctrlrConfig *config.ControllerConfig, kubeconfigOverwrite bool, ociImage string, initWait time.Duration) *Cluster {
	//oxideConfig *oxide.Config, projectID string, controlPlanePrefix, workerPrefix string, controlPlaneCount, workerCount int, controlPlaneSpec, workerSpec NodeSpec, imageParallelism int, namespace, secretName string, pubkey []byte, clusterInitWait time.Duration, kubeconfigOverwrite bool, tailscaleAPIKey, tailscaleTailnet, OCIimage string)
	c := &Cluster{
		logger: logger.WithField("component", "cluster"),
		config: ctrlrConfig,
		oxideConfig: &oxide.Config{
			Token: ctrlrConfig.OxideToken,
			Host:  ctrlrConfig.OxideURL,
		},
		kubeconfigOverwrite: kubeconfigOverwrite,
		ociImage:            ociImage,
		initWait:            initWait,
	}
	return c
}

// ensureClusterExists checks if a k3s cluster exists, and creates one if needed
func (c *Cluster) ensureClusterExists(ctx context.Context) (newKubeconfig []byte, err error) {
	// local vars just for convenience
	client, err := oxide.NewClient(c.oxideConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Oxide API client: %v", err)
	}
	projectID := c.projectID
	controlPlanePrefix := c.config.ControlPlaneSpec.Prefix
	controlPlaneCount := c.config.ControlPlaneCount
	secretName := c.config.SecretName

	c.logger.Debugf("Checking if control plane IP %s exists", controlPlanePrefix)
	controlPlaneIP, err := c.ensureControlPlaneIP(ctx, controlPlanePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get control plane IP: %w", err)
	}

	if c.config.ControlPlaneIP == "" {
		c.config.ControlPlaneIP = controlPlaneIP.Ip
	}

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

	if len(controlPlaneNodes) > 0 && c.apiConfig != nil && c.apiConfig.Source == ConfigSourceProvidedKubeconfig {
		c.logger.Debugf("Found %d control plane nodes, but kubeconfig already exists", len(controlPlaneNodes))
		// TODO: check to see if it can access the cluster
	}

	if len(controlPlaneNodes) == 0 && c.apiConfig != nil && c.apiConfig.Source == ConfigSourceProvidedKubeconfig && !c.kubeconfigOverwrite {
		c.logger.Debugf("Found no control plane nodes, but kubeconfig already exists")
		return nil, fmt.Errorf("kubeconfig already exists but cluster does not")
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
		highest++
		secrets := make(map[string][]byte)
		priv, pub, err := util.SSHKeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate SSH key pair: %w", err)
		}
		var pubkeyList []string
		if c.config.UserSSHPublicKey != "" {
			pubkeyList = append(pubkeyList, c.config.UserSSHPublicKey)
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
		hostname := instances[0].Hostname
		// if the control plane node was not configured to have an external IP,
		// attach the floating IP to start and use that is the externalIP
		var (
			externalIP  string
			fipAttached bool
		)
		if c.config.ControlPlaneSpec.ExternalIP {
			c.logger.Debugf("Control plane node %s has external IP, using that", hostid)
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
			c.logger.Debugf("Control plane node %s does not have external IP, attaching and using floating IP", hostid)
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
					c.logger.Debugf("Failed to attach floating IP %v, retrying %d/%d", err, i+1, maxTries)
					continue
				}
				c.logger.Debug("Successfully attached floating IP")
				attached = true
				break
			}
			if !attached {
				return nil, fmt.Errorf("failed to attach floating IP after %d tries, last error: %w", maxTries, err)
			}
			externalIP = controlPlaneIP.Ip
			fipAttached = true
		}

		// if tailscale is used and an external IP is not available, then use that to get onto the control plane node
		clusterAccessIP := externalIP

		// wait for the control plane node to be up and running
		timeLeft := c.initWait
		for {
			if timeLeft <= 0 {
				c.logger.Errorf("Control plane at %s did not respond in time, exiting", clusterAccessIP)
				return nil, fmt.Errorf("control plane at %s did not respond in time", clusterAccessIP)
			}

			c.logger.Infof("Waiting %s for control plane node to be up and running...", timeLeft)
			sleepTime := 30 * time.Second
			time.Sleep(sleepTime)
			timeLeft -= sleepTime

			if c.config.TailscaleAPIKey != "" {
				c.logger.Infof("Checking if control plane node has joined tailnet")
				client := &tailscale.Client{
					Tailnet: c.config.TailscaleTailnet,
					APIKey:  c.config.TailscaleAPIKey,
				}
				ctx := context.Background()
				devices, err := client.Devices().List(ctx)
				if err != nil {
					return nil, fmt.Errorf("failed to list tailscale devices: %w", err)
				}
				// find the most recent device
				var validIP, validHostname string
				for _, device := range devices {
					if device.Hostname == hostname {
						c.logger.Debugf("Found tailscale device %s matches our hostname %s", device.Hostname, hostname)
						for _, addr := range device.Addresses {
							// check if it is a valid IPv4 address
							if isCanonicalIPv4(addr) {
								validIP = addr
								validHostname = device.Hostname
								break
							}
						}
					}
				}
				if validIP == "" {
					c.logger.Debugf("no valid tailscale device found yet for hostname %s", hostname)
					continue
				}
				c.logger.Debugf("Found tailscale device %s with IP %s", validHostname, validIP)
				clusterAccessIP = validIP
				break
			}

			if isClusterAlive(fmt.Sprintf("https://%s:%d", clusterAccessIP, 6443)) {
				c.logger.Infof("Control plane at %s is up and running", clusterAccessIP)
				break
			}
		}
		// attach the floating IP to the control plane node, if not done already
		if !fipAttached {
			c.logger.Debugf("Control plane node %s did not have floating IP attached, attaching", hostid)
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
		joinToken, err := util.RunSSHCommand("root", fmt.Sprintf("%s:22", clusterAccessIP), priv, "cat /var/lib/rancher/k3s/server/node-token")
		if err != nil {
			return nil, fmt.Errorf("failed to run command to retrieve join token on control plane node: %w", err)
		}
		// save the private key and public key to the secret
		c.config.K3sJoinToken = string(joinToken)
		c.config.SystemSSHPublicKey = string(pub)
		c.config.SystemSSHPrivateKey = string(priv)

		// get the kubeconfig
		kubeconfig, err := util.RunSSHCommand("root", fmt.Sprintf("%s:22", clusterAccessIP), priv, "cat /etc/rancher/k3s/k3s.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to run command to retrieve kubeconfig on control plane node: %w", err)
		}

		c.logger.Debugf("retrieved new kubeconfig of size %d", len(kubeconfig))

		// have to change the kubeconfig to use the floating IP
		kubeconfigString := string(kubeconfig)
		re := regexp.MustCompile(`(server:\s*\w+://)(\d+\.\d+\.\d+\.\d+)(:\d+)`)
		kubeconfigString = re.ReplaceAllString(kubeconfigString, fmt.Sprintf("${1}%s${3}", clusterAccessIP))

		newKubeconfig = []byte(kubeconfigString)

		// get a Kubernetes client
		apiConfig, err := GetRestConfig(newKubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get rest config: %w", err)
		}
		c.apiConfig = apiConfig
		clientset, err := getClientset(c.apiConfig.Config)
		if err != nil {
			return nil, fmt.Errorf("unable to get Kubernetes clientset: %w", err)
		}
		c.clientset = clientset

		// ensure we have the namespace we need
		namespace := c.config.ControlPlaneNamespace
		if err := createNamespace(ctx, clientset, namespace); err != nil {
			return nil, fmt.Errorf("failed to create namespace: %w", err)
		}

		configJson, err := c.config.ToJSON()
		if err != nil {
			return nil, fmt.Errorf("failed to convert config to JSON: %w", err)
		}
		secrets[secretKeyConfig] = configJson

		// save the config to the Kubernetes secret
		c.logger.Debugf("Saving secret %s/%s to Kubernetes", namespace, secretName)
		if err := saveSecret(ctx, clientset, c.logger, namespace, secretName, secrets); err != nil {
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
	return newKubeconfig, nil
}

func isCanonicalIPv4(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	ipv4 := ip.To4()
	// Ensure it's truly an IPv4 (not IPv6-mapped) and in canonical form
	return ipv4 != nil && ipv4.String() == s
}
