package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oxidecomputer/oxide.go/oxide"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	KB = 1024
	MB = 1024 * KB
	GB = 1024 * MB

	blockSize = 4096

	secretKeyUserSSH          = "user-ssh-public-key"
	secretKeyJoinToken        = "k3s-join-token"
	secretKeySystemSSHPublic  = "system-ssh-public-key"
	secretKeySystemSSHPrivate = "system-ssh-private-key"
)

var (
	oxideAPIURL             string
	tokenFilePath           string
	clusterProject          string
	controlPlanePrefix      string
	controlPlaneCount       int
	controlPlaneImageName   string
	controlPlaneImageSource string
	workerImageName         string
	workerImageSource       string
	controlPlaneMemory      uint64
	workerMemory            uint64
	controlPlaneCPU         uint16
	workerCPU               uint16
	clusterInitWait         int
	userSSHPublicKey        string
	kubeconfigPath          string
	controlPlaneSecret      string
)

// Node represents a Kubernetes node
type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type Image struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// loadOxideToken retrieves the API token from persistent storage
func loadOxideToken() (string, error) {
	data, err := os.ReadFile(tokenFilePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func ensureControlPlaneIP(ctx context.Context, client *oxide.Client, projectID string) (*oxide.FloatingIp, error) {
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

// getSecret gets the secret with all of our important information
func getSecret(ctx context.Context, kubeconfigRaw []byte, secret string) (map[string][]byte, error) {
	parts := strings.SplitN(secret, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid secret format %s, expected <namespace>/<name>", secret)
	}
	namespace, name := parts[0], parts[1]

	clientset, err := getClientset(kubeconfigRaw)
	if err != nil {
		return nil, err
	}

	// Get the secret
	secretObj, err := clientset.CoreV1().Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return secretObj.Data, nil
}

// getClientset returns a Kubernetes clientset, optionally using raw kubeconfig data.
// If kubeconfigRaw is nil or empty, it falls back to environment/default paths and in-cluster config.
func getClientset(kubeconfigRaw []byte) (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	if len(kubeconfigRaw) > 0 {
		// Load from raw kubeconfig bytes
		configAPI, err := clientcmd.Load(kubeconfigRaw)
		if err != nil {
			return nil, err
		}
		clientConfig := clientcmd.NewDefaultClientConfig(*configAPI, &clientcmd.ConfigOverrides{})
		config, err = clientConfig.ClientConfig()
		if err != nil {
			return nil, err
		}
	} else {
		// Try default loading rules (KUBECONFIG env var, ~/.kube/config, etc.)
		kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		)
		config, err = kubeconfig.ClientConfig()
		if err != nil {
			// Fall back to in-cluster config
			config, err = rest.InClusterConfig()
			if err != nil {
				return nil, fmt.Errorf("could not load kubeconfig from file/env or in-cluster")
			}
		}
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// saveSecret save a secret to the Kubernetes cluster
func saveSecret(secretRef string, kubeconfig []byte, data map[string][]byte) error {
	// Parse namespace and name from <namespace>/<name>
	parts := strings.SplitN(secretRef, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid secret reference: expected <namespace>/<name>")
	}
	namespace, name := parts[0], parts[1]

	clientset, err := getClientset(kubeconfig)
	if err != nil {
		return err
	}

	// Prepare the secret object
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
		Type: v1.SecretTypeOpaque,
	}

	secretsClient := clientset.CoreV1().Secrets(namespace)

	// Check if the secret exists
	_, err = secretsClient.Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			_, err = secretsClient.Create(context.TODO(), secret, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Update existing secret
	_, err = secretsClient.Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}

// getSecretValue retrieves a specific value from the secret
func getSecretValue(ctx context.Context, kubeconfig []byte, secret, key string) ([]byte, error) {
	secretData, err := getSecret(ctx, kubeconfig, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	value, ok := secretData[key]
	if !ok {
		return nil, fmt.Errorf("key '%s' not found in secret", key)
	}
	decodedValue, err := base64.StdEncoding.DecodeString(string(value))
	if err != nil {
		return nil, fmt.Errorf("failed to decode value: %w", err)
	}
	return decodedValue, nil
}

// getJoinToken retrieves a new k3s worker join token from the Kubernetes cluster
func getJoinToken(ctx context.Context, kubeconfig []byte, secret string) (string, error) {
	value, err := getSecretValue(ctx, kubeconfig, secret, secretKeyJoinToken)
	if err != nil {
		return "", fmt.Errorf("failed to get join token: %w", err)
	}
	// convert to string
	return string(value), nil
}

// getUserSSHPublicKey retrieves the SSH public key from the Kubernetes cluster
func getUserSSHPublicKey(ctx context.Context, kubeconfig []byte, secret string) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, kubeconfig, secret, secretKeyUserSSH)
	if err != nil {
		return nil, fmt.Errorf("failed to get user SSH public key: %w", err)
	}
	return pubkey, nil
}

// generateCloudConfig for a particular node type
func generateCloudConfig(nodeType string, initCluster bool, controlPlaneIP, joinToken string, pubkey []string) string {
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
		log.Fatalf("Unknown node type: %s", nodeType)
	}
	return fmt.Sprintf(`
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
}

// ensureImagesExist checks if the right images exist and creates them if needed
// they can exist at the silo or project level. However, if they do not exist, then they
// will be created at the project level.
func ensureImagesExist(ctx context.Context, client *oxide.Client, projectID string, images ...Image) ([]string, error) {
	// TODO: We don't need to list images, we can `View` them by name -
	//       `images` array is never long, few more requests shouldn't harm.
	// TODO: Do we need pagination? Using arbitrary limit for now.
	existing, err := client.ImageList(ctx, oxide.ImageListParams{Project: oxide.NameOrId(projectID), Limit: oxide.NewPointer(32)})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	var (
		missingImages []Image
		imageMap      = make(map[string]string)
		idMap         = make(map[string]string)
	)
	for _, image := range existing.Items {
		imageMap[string(image.Name)] = image.Id
	}
	for _, image := range images {
		if _, ok := imageMap[image.Name]; !ok {
			missingImages = append(missingImages, image)
		} else {
			idMap[image.Name] = imageMap[image.Name]
		}
	}

	for _, missingImage := range missingImages {
		snapshotName := fmt.Sprintf("snapshot-%s", missingImage.Name)
		// how to create the image? oxide makes this a bit of a pain, you need to do multiple steps:
		// 1. Download the image from the URL locally to a temporary file
		// 2. Determine the size of the image
		// 3. Create a blank disk of that size or larger, rounded up to nearest block size at least
		//      https://docs.oxide.computer/api/disk_create
		// 4. Import base64 blobs of data from the disk to the blank disk
		//      https://docs.oxide.computer/api/disk_bulk_write_import_start
		//      https://docs.oxide.computer/api/disk_bulk_write_import
		// 	    https://docs.oxide.computer/api/disk_bulk_write_import_stop
		// 5. Finalize the import by making a snapshot of the disk
		//      https://docs.oxide.computer/api/disk_finalize_import
		// 6. Create an image from the snapshot
		//      https://docs.oxide.computer/api/image_create
		file, err := os.CreateTemp("", "image-")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary file: %w", err)
		}
		defer os.RemoveAll(file.Name())
		if err := downloadFile(file.Name(), missingImage.Source); err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		stat, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to get file size: %w", err)
		}
		size := stat.Size()
		if size == 0 {
			return nil, fmt.Errorf("image file is empty")
		}
		// FIXME?: round to the nearest GB: not verified - the default debian image is *exactly* 3GB :)
		//   https://github.com/oxidecomputer/oxide.rs/blob/17e3f58248832f977d366afdc69641551a62b1db/sdk/src/extras/disk.rs#L735
		// round up to nearest block size
		size = (size + blockSize) &^ blockSize
		// create the disk
		disk, err := client.DiskCreate(ctx, oxide.DiskCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.DiskCreate{
				Description: fmt.Sprintf("Disk for image '%s'", missingImage.Name),
				Size:        oxide.ByteCount(size),
				Name:        oxide.Name(missingImage.Name),
				DiskSource: oxide.DiskSource{
					Type:      oxide.DiskSourceTypeImportingBlocks,
					BlockSize: blockSize, // TODO: Must be multiple of image size. Verify?
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create disk: %w", err)
		}
		// import the data
		if err := client.DiskBulkWriteImportStart(ctx, oxide.DiskBulkWriteImportStartParams{
			Disk: oxide.NameOrId(disk.Id),
		}); err != nil {
			return nil, fmt.Errorf("failed to start bulk write import: %w", err)
		}
		// write in 0.5MB chunks or until finished
		f, err := os.Open(file.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()
		var offset int
		for {
			buf := make([]byte, MB/2)
			n, err := f.Read(buf)
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			if n == 0 {
				break
			}
			// convert the read data into base64. Why? because that is what oxide wants
			if err := client.DiskBulkWriteImport(ctx, oxide.DiskBulkWriteImportParams{
				Disk: oxide.NameOrId(disk.Id),
				Body: &oxide.ImportBlocksBulkWrite{
					Base64EncodedData: base64.StdEncoding.EncodeToString(buf[:n]),
					Offset:            &offset,
				},
			}); err != nil {
				return nil, fmt.Errorf("failed to write data: %w", err)
			}
			offset += n
		}
		if err := client.DiskBulkWriteImportStop(ctx, oxide.DiskBulkWriteImportStopParams{
			Disk: oxide.NameOrId(disk.Id),
		}); err != nil {
			return nil, fmt.Errorf("failed to stop bulk write import: %w", err)
		}
		// finalize the import
		if err := client.DiskFinalizeImport(ctx, oxide.DiskFinalizeImportParams{
			Disk: oxide.NameOrId(disk.Id),
			Body: &oxide.FinalizeDisk{
				SnapshotName: oxide.Name(snapshotName),
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to finalize import: %w", err)
		}

		client.DiskDelete(ctx, oxide.DiskDeleteParams{
			Disk: oxide.NameOrId(disk.Id),
		})

		// Find snapshot Id by name.
		snapshot, err := client.SnapshotView(ctx, oxide.SnapshotViewParams{
			Snapshot: oxide.NameOrId(snapshotName), Project: oxide.NameOrId(projectID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to find snapshot: %w", err)
		}

		image, err := client.ImageCreate(ctx, oxide.ImageCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.ImageCreate{
				Name:        oxide.Name(missingImage.Name),
				Description: fmt.Sprintf("Image for '%s'", missingImage.Name),
				Source: oxide.ImageSource{
					Type: oxide.ImageSourceTypeSnapshot,
					Id:   snapshot.Id,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create image: %w", err)
		}
		imageMap[missingImage.Name] = image.Id
		idMap[missingImage.Name] = image.Id
	}

	// go through all of the image names and get their IDs
	var ids []string
	for _, image := range images {
		if id, ok := imageMap[image.Name]; !ok {
			return nil, fmt.Errorf("image '%s' does not exist", image.Name)
		} else {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// downloadFile downloads a file from a URL and saves it to the local filesystem
// It should understand different file URL schemes, but for now, just knows https
func downloadFile(filepath, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if _, err := f.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// ensureProjectExists checks if the right project exists and returns its ID
func ensureProjectExists(ctx context.Context, client *oxide.Client) (string, error) {
	// TODO: We don't need to list Projects to find specific one, we can `View`
	//       it by name.
	// TODO: do we need pagination? Using arbitrary limit for now.
	projects, err := client.ProjectList(ctx, oxide.ProjectListParams{Limit: oxide.NewPointer(32)})
	if err != nil {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}

	var projectID string
	for _, project := range projects.Items {
		if string(project.Name) == clusterProject {
			log.Printf("Cluster project '%s' exists.", clusterProject)
			projectID = project.Id
			break
		}
	}

	if projectID == "" {
		log.Printf("Cluster project '%s' does not exist. Creating it...", clusterProject)
		newProject, err := client.ProjectCreate(ctx, oxide.ProjectCreateParams{
			Body: &oxide.ProjectCreate{Name: oxide.Name(clusterProject)},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create project: %w", err)
		}
		projectID = newProject.Id
		log.Printf("Created project '%s' with ID '%s'", clusterProject, projectID)
	}
	return projectID, nil
}

// createControlPlaneNodes creates new control plane nodes
func createControlPlaneNodes(ctx context.Context, initCluster bool, count, start int, controlPlaneIP string, client *oxide.Client, projectID, joinToken string, pubkey []string) ([]oxide.Instance, error) {
	var controlPlaneNodes []oxide.Instance
	cloudConfig := generateCloudConfig("server", initCluster, controlPlaneIP, joinToken, pubkey)
	for i := start; i < count; i++ {
		instance, err := client.InstanceCreate(ctx, oxide.InstanceCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.InstanceCreate{
				Name:   oxide.Name(fmt.Sprintf("%s%d", controlPlanePrefix, i)),
				Memory: oxide.ByteCount(controlPlaneMemory * GB),
				Ncpus:  oxide.InstanceCpuCount(controlPlaneCPU),
				BootDisk: &oxide.InstanceDiskAttachment{
					DiskSource: oxide.DiskSource{
						Type:      oxide.DiskSourceTypeImage,
						ImageId:   controlPlaneImageName,
						BlockSize: blockSize, // TODO: Must be multiple of image size. Verify?
					},
				},
				UserData: cloudConfig,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create control plane node: %w", err)
		}
		controlPlaneNodes = append(controlPlaneNodes, *instance)
	}
	return controlPlaneNodes, nil
}

// generateEphemeralSSHKeyPair generates a new ephemeral SSH key pair
func generateEphemeralSSHKeyPair() ([]byte, []byte, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	// Convert to OpenSSH public key format
	pubKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}

	// Encode private key to PEM (OpenSSH format typically uses its own encoding,
	// but we’ll use PEM for demonstration; OpenSSH private keys are usually stored
	// in a different format via ssh-keygen)
	privPEM := new(bytes.Buffer)
	err = pem.Encode(privPEM, &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: priv, // raw private key bytes
	})
	if err != nil {
		return nil, nil, err
	}
	return privPEM.Bytes(), ssh.MarshalAuthorizedKey(pubKey), nil
}

// isClusterAlive checks if the cluster is alive by sending a request to the API server
// healthz endpoint
func isClusterAlive(apiServerURL string) bool {
	client := &http.Client{
		Timeout: 3 * time.Second,
		// Kubernetes API server uses self-signed certs by default
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get(apiServerURL + "/healthz")
	if err != nil {
		fmt.Println("Error contacting cluster:", err)
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// runSSHCommand run a command on a remote server via SSH
func runSSHCommand(user, addr string, privPEM []byte, command string) ([]byte, error) {
	// Parse the private key
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Create SSH client config
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // for testing only; validate host keys in production
	}

	// Connect to the SSH server
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SSH: %w", err)
	}
	defer client.Close()

	// Start a session
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Capture stdout
	output, err := session.Output(command)
	if err != nil {
		return nil, fmt.Errorf("command failed: %w", err)
	}

	return output, nil
}

// ensureClusterExists checks if a k3s cluster exists, and creates one if needed
func ensureClusterExists(ctx context.Context, client *oxide.Client, projectID string, kubeconfig, userPubKey []byte) error {
	// TODO: endpoint is paginated, using arbitrary limit for now.
	instances, err := client.InstanceList(ctx, oxide.InstanceListParams{
		Project: oxide.NameOrId(projectID),
		Limit:   oxide.NewPointer(32),
	})
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}

	var controlPlaneNodes []oxide.Instance
	for _, instance := range instances.Items {
		if strings.HasPrefix(string(instance.Name), controlPlanePrefix) {
			controlPlaneNodes = append(controlPlaneNodes, instance)
		}
	}

	// if we have enough nodes, return
	if len(controlPlaneNodes) >= controlPlaneCount {
		return nil
	}

	controlPlaneIP, err := ensureControlPlaneIP(ctx, client, projectID)
	if err != nil {
		return fmt.Errorf("failed to get control plane IP: %w", err)
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
			return fmt.Errorf("kubeconfig already exists but cluster does not")
		}
		highest++
		secrets := make(map[string][]byte)
		priv, pub, err := generateEphemeralSSHKeyPair()
		if err != nil {
			return fmt.Errorf("failed to generate SSH key pair: %w", err)
		}
		var pubkeyList []string
		if userPubKey != nil {
			pubkeyList = append(pubkeyList, string(userPubKey))
		}
		pubkeyList = append(pubkeyList, string(pub))
		// add the public key to the node in addition to the user one
		instances, err := createControlPlaneNodes(ctx, true, 1, highest, controlPlaneIP.Ip, client, projectID, "", pubkeyList)
		if err != nil {
			return fmt.Errorf("failed to create control plane node: %w", err)
		}
		if len(instances) < 1 {
			return fmt.Errorf("created 0 control plane nodes")
		}
		hostid := instances[0].Id
		ipList, err := client.InstanceExternalIpList(ctx, oxide.InstanceExternalIpListParams{
			Instance: oxide.NameOrId(hostid),
			Project:  oxide.NameOrId(projectID),
		})
		if err != nil {
			return fmt.Errorf("failed to get external IP list: %w", err)
		}
		if len(ipList.Items) < 1 {
			return fmt.Errorf("created control plane node has no external IP")
		}
		externalIP := ipList.Items[0].Ip
		// wait for the control plane node to be up and running
		timeLeft := time.Duration(clusterInitWait) * time.Minute
		for {
			log.Printf("Waiting %d mins for control plane node %s to be up and running...", timeLeft, controlPlaneIP)
			sleepTime := 1 * time.Minute
			time.Sleep(sleepTime)
			timeLeft -= sleepTime
			if isClusterAlive(fmt.Sprintf("https://%s:%d", externalIP, 6443)) {
				log.Printf("Control plane at %s is up and running", externalIP)
				break
			}
			if timeLeft <= 0 {
				log.Printf("Control plane at %s did not respond in time, exiting", externalIP)
				return fmt.Errorf("control plane at %s did not respond in time", externalIP)
			}
		}
		// attach the floating IP to the control plane node
		if _, err := client.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
			FloatingIp: oxide.NameOrId(controlPlaneIP.Id),
			Project:    oxide.NameOrId(projectID),
			Body: &oxide.FloatingIpAttach{
				Kind:   oxide.FloatingIpParentKindInstance,
				Parent: oxide.NameOrId(hostid),
			},
		}); err != nil {
			return fmt.Errorf("failed to attach floating IP: %w", err)
		}

		// get the join token and save it to our secrets map
		joinToken, err := runSSHCommand("root", fmt.Sprintf("%s:22", externalIP), priv, "cat /var/lib/rancher/k3s/server/node-token")
		if err != nil {
			return fmt.Errorf("failed to run command to retrieve join token on control plane node: %w", err)
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
		kubeconfig, err = runSSHCommand("root", fmt.Sprintf("%s:22", externalIP), priv, "cat /etc/rancher/k3s/k3s.yaml")
		if err != nil {
			return fmt.Errorf("failed to run command to retrieve kubeconfig on control plane node: %w", err)
		}

		if err := saveFileIfNotExists(kubeconfigPath, kubeconfig); err != nil {
			return fmt.Errorf("failed to save kubeconfig: %w", err)
		}

		// save the join token, system ssh key pair, user ssh key to the Kubernetes secret
		if err := saveSecret(controlPlaneSecret, kubeconfig, secrets); err != nil {
			return fmt.Errorf("failed to save secret: %w", err)
		}

		controlPlaneNodes = append(controlPlaneNodes, instances[0])
	}

	// now that we have an initial node, we can get the join token
	joinToken, err := getJoinToken(ctx, kubeconfig, controlPlaneSecret)
	if err != nil {
		return fmt.Errorf("failed to retrieve join token: %v", err)
	}
	// the number we want is the next one
	highest++
	count := controlPlaneCount - len(controlPlaneNodes)
	start := highest
	if _, err := createControlPlaneNodes(ctx, false, count, start, controlPlaneIP.Ip, client, projectID, joinToken, []string{string(userPubKey)}); err != nil {
		return fmt.Errorf("failed to create control plane node: %w", err)
	}

	return nil
}

// saveFileIfNotExists saves a file to the specified path if it does not already exist
func saveFileIfNotExists(path string, data []byte) error {
	// Check if the file exists
	if _, err := os.Stat(path); err == nil {
		// File exists
		return fmt.Errorf("file already exists")
	} else if !os.IsNotExist(err) {
		// Some other error while checking
		return err
	}

	// Write the file if it doesn't exist
	return os.WriteFile(path, data, 0644)
}

// handleAddNode creates a new worker node
func handleAddNode(secret string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("Processing request to add a worker node...")
		ctx := r.Context()

		token, err := loadOxideToken()
		if err != nil {
			log.Printf("Failed to load Oxide API token: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		cfg := oxide.Config{
			Host:  oxideAPIURL,
			Token: token,
		}
		client, err := oxide.NewClient(&cfg)
		if err != nil {
			log.Printf("Failed to create Oxide API client: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		projectID := clusterProject
		controlPlaneIP, err := ensureControlPlaneIP(ctx, client, projectID)
		if err != nil {
			log.Printf("Failed to retrieve control plane IP: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}

		joinToken, err := getJoinToken(ctx, nil, secret)
		if err != nil {
			log.Printf("Failed to retrieve join token: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		pubkey, err := getUserSSHPublicKey(ctx, nil, secret)
		if err != nil {
			log.Printf("Failed to retrieve SSH public key: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		workerName := fmt.Sprintf("worker-%d", time.Now().Unix())
		cloudConfig := generateCloudConfig("agent", false, controlPlaneIP.Ip, joinToken, []string{string(pubkey)})
		log.Printf("Creating worker node: %s", workerName)
		_, err = client.InstanceCreate(ctx, oxide.InstanceCreateParams{
			Project: oxide.NameOrId(clusterProject),
			Body: &oxide.InstanceCreate{
				Name:   oxide.Name(workerName),
				Memory: oxide.ByteCount(workerMemory),
				Ncpus:  oxide.InstanceCpuCount(workerCPU),
				BootDisk: &oxide.InstanceDiskAttachment{
					DiskSource: oxide.DiskSource{
						Type:      oxide.DiskSourceTypeImage,
						ImageId:   workerImageName,
						BlockSize: blockSize, // TODO: Must be multiple of image size. Verify?
					},
				},
				UserData: cloudConfig,
			},
		})
		if err != nil {
			log.Printf("Failed to create worker node: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Worker node added"))
	}
}

func initializeSetup(kubeconfig, pubkey []byte) error {
	ctx := context.Background()

	token, err := loadOxideToken()
	if err != nil {
		return fmt.Errorf("failed to load Oxide API token: %v", err)
	}

	cfg := oxide.Config{
		Host:  oxideAPIURL,
		Token: token,
	}
	client, err := oxide.NewClient(&cfg)
	if err != nil {
		return fmt.Errorf("failed to create Oxide API client: %v", err)
	}

	projectID, err := ensureProjectExists(ctx, client)
	if err != nil {
		return fmt.Errorf("project verification failed: %v", err)
	}

	if _, err := ensureImagesExist(ctx, client, projectID, Image{controlPlaneImageName, controlPlaneImageSource}, Image{workerImageName, workerImageSource}); err != nil {
		return fmt.Errorf("image verification failed: %v", err)
	}

	if err := ensureClusterExists(ctx, client, projectID, kubeconfig, pubkey); err != nil {
		return fmt.Errorf("cluster verification failed: %v", err)
	}

	return nil
}

// loadFile loads the contents from the specified path
// and returns the key material as a byte slice.
// If the path is empty, it returns an empty byte slice and no error.
// If the file cannot be read, it returns an error.
func loadFile(p string) ([]byte, error) {
	if p == "" {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "node-manager",
		Short: "Node Management Service",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Starting Node Management Service...")

			// load the ssh key provided, if any
			// loadSSHKey returns empty key material and no error if the userSSHPublicKey is empty
			pubkey, err := loadFile(userSSHPublicKey)
			if err != nil {
				return fmt.Errorf("failed to load ssh public key at %s: %w", userSSHPublicKey, err)
			}

			kubeconfig, err := loadFile(kubeconfigPath)
			if err != nil {
				return fmt.Errorf("failed to load kubeconfig at %s: %w", kubeconfigPath, err)
			}

			if err := initializeSetup(kubeconfig, pubkey); err != nil {
				return fmt.Errorf("failed to initialize setup: %v", err)
			}

			// Define API routes
			http.HandleFunc("/nodes/add", handleAddNode(controlPlaneSecret))

			// Start HTTP server
			log.Println("API listening on port 8080")
			return http.ListenAndServe(":8080", nil)
		},
	}

	// Define CLI flags
	rootCmd.Flags().StringVar(&oxideAPIURL, "oxide-api-url", "https://oxide-api.example.com", "Oxide API base URL")
	rootCmd.Flags().StringVar(&tokenFilePath, "token-file", "/data/oxide_token", "Path to Oxide API token file")
	rootCmd.Flags().StringVar(&clusterProject, "cluster-project", "ainekko-cluster", "Oxide project name for Kubernetes cluster")
	rootCmd.Flags().StringVar(&controlPlanePrefix, "control-plane-prefix", "ainekko-control-plane-", "Prefix for control plane instances")
	rootCmd.Flags().IntVar(&controlPlaneCount, "control-plane-count", 3, "Number of control plane instances to maintain")
	rootCmd.Flags().StringVar(&controlPlaneImageName, "control-plane-image-name", "debian-12-cloud", "Image to use for control plane instances")
	rootCmd.Flags().StringVar(&controlPlaneImageSource, "control-plane-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for control plane instances")
	rootCmd.Flags().StringVar(&workerImageName, "worker-image", "debian-12-cloud", "Image to use for worker nodes")
	rootCmd.Flags().StringVar(&workerImageSource, "worker-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for worker instances")
	rootCmd.Flags().Uint64Var(&controlPlaneMemory, "control-plane-memory", 4, "Memory to allocate to each control plane node, in GB")
	rootCmd.Flags().Uint64Var(&workerMemory, "worker-memory", 16, "Memory to allocate to each worker node, in GB")
	rootCmd.Flags().Uint16Var(&controlPlaneCPU, "control-plane-cpu", 2, "vCPU count to allocate to each control plane node")
	rootCmd.Flags().Uint16Var(&workerCPU, "worker-cpu", 4, "vCPU count to allocate to each worker node")
	rootCmd.Flags().IntVar(&clusterInitWait, "cluster-init-wait", 5, "Time to wait for the first control plane node to be up and running (in minutes)")
	rootCmd.Flags().StringVar(&userSSHPublicKey, "user-ssh-public-key", "", "Path to public key to inject in all deployed cloud instances")
	rootCmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "~/.kube/oxide-controller-config", "Path to save kubeconfig when generating new cluster, or to use for accessing existing cluster")
	rootCmd.Flags().StringVar(&controlPlaneSecret, "control-plane-secret", "kube-system/oxide-controller-secret", "secret in Kubernetes cluster where the following are stored: join token, user ssh public key, controller ssh private/public keypair; should be as <namespace>/<name>")

	// Execute CLI
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
	}
}
