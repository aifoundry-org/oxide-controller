package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oxidecomputer/oxide.go/oxide"
	"github.com/spf13/cobra"
)

const (
	KB = 1024
	MB = 1024 * KB
	GB = 1024 * MB

	blockSize = 4096
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
	return string(data), nil
}

func getControlPlaneIP(ctx context.Context, client *oxide.Client, projectID string) (string, error) {
	var controlPlaneIP string
	fips, err := client.FloatingIpList(ctx, oxide.FloatingIpListParams{Project: oxide.NameOrId(projectID)})
	if err != nil {
		return "", fmt.Errorf("failed to list floating IPs: %w", err)
	}
	for _, fip := range fips.Items {
		if strings.HasPrefix(string(fip.Name), controlPlanePrefix) {
			controlPlaneIP = fip.Ip
			break
		}
	}
	return controlPlaneIP, nil
}

// getJoinToken retrieves a new k3s worker join token from a control plane instance
func getJoinToken(ctx context.Context, controlPlaneIP string) (string, error) {
	log.Printf("Fetching join token from control plane at %s", controlPlaneIP)
	url := fmt.Sprintf("http://%s:6443/v1-k3s/server-token", controlPlaneIP)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch join token: %w", err)
	}
	defer resp.Body.Close()

	var token string
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", fmt.Errorf("failed to parse join token response: %w", err)
	}

	return token, nil
}

// generateCloudConfig for a particular node type
func generateCloudConfig(nodeType string, initCluster bool, controlPlaneIP, joinToken string) string {
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
runcmd:
  - curl -sfL https://get.k3s.io | sh -s - %s %s %s %s %s %s
`, typeFlag, initFlag, sanFlag, tokenFlag, serverFlag, sanFlag)
}

// ensureImagesExist checks if the right images exist and creates them if needed
// they can exist at the silo or project level. However, if they do not exist, then they
// will be created at the project level.
func ensureImagesExist(ctx context.Context, client *oxide.Client, projectID string, images ...Image) ([]string, error) {
	// TODO: Do we need pagination? Using arbitrary limit for now.
	existing, err := client.ImageList(ctx, oxide.ImageListParams{Project: oxide.NameOrId(projectID), Limit: 32})
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
			Project: oxide.NameOrId(projectID),
			Disk:    oxide.NameOrId(disk.Id),
		}); err != nil {
			return nil, fmt.Errorf("failed to start bulk write import: %w", err)
		}
		// write in 1MB chunks or until finished
		f, err := os.Open(file.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()
		var offset int
		for {
			buf := make([]byte, MB)
			n, err := f.Read(buf)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			if n == 0 {
				break
			}
			// convert the read data into base64. Why? because that is what oxide wants
			if err := client.DiskBulkWriteImport(ctx, oxide.DiskBulkWriteImportParams{
				Project: oxide.NameOrId(projectID),
				Disk:    oxide.NameOrId(disk.Id),
				Body: &oxide.ImportBlocksBulkWrite{
					Base64EncodedData: base64.StdEncoding.EncodeToString(buf[:n]),
					Offset:            offset,
				},
			}); err != nil {
				return nil, fmt.Errorf("failed to write data: %w", err)
			}
			offset += n
		}
		if err := client.DiskBulkWriteImportStop(ctx, oxide.DiskBulkWriteImportStopParams{
			Project: oxide.NameOrId(projectID),
			Disk:    oxide.NameOrId(disk.Id),
		}); err != nil {
			return nil, fmt.Errorf("failed to stop bulk write import: %w", err)
		}
		// finalize the import
		if err := client.DiskFinalizeImport(ctx, oxide.DiskFinalizeImportParams{
			Disk:    oxide.NameOrId(disk.Id),
			Project: oxide.NameOrId(projectID),
			Body: &oxide.FinalizeDisk{
				SnapshotName: oxide.Name(snapshotName),
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to finalize import: %w", err)
		}

		client.DiskDelete(ctx, oxide.DiskDeleteParams{
			Project: oxide.NameOrId(projectID),
			Disk:    oxide.NameOrId(disk.Id),
		})

		image, err := client.ImageCreate(ctx, oxide.ImageCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.ImageCreate{
				Name:        oxide.Name(missingImage.Name),
				Description: fmt.Sprintf("Image for '%s'", missingImage.Name),
				Source: oxide.ImageSource{
					Type: oxide.ImageSourceTypeSnapshot,
					Id:   snapshotName,
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
	// TODO: do we need pagination? Using arbitrary limit for now.
	projects, err := client.ProjectList(ctx, oxide.ProjectListParams{Limit: 32})
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

// ensureClusterExists checks if a k3s cluster exists, and creates one if needed
func ensureClusterExists(ctx context.Context, client *oxide.Client, projectID string) error {
	instances, err := client.InstanceList(ctx, oxide.InstanceListParams{Project: oxide.NameOrId(projectID)})
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

	controlPlaneIP, err := getControlPlaneIP(ctx, client, projectID)
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
	joinToken, err := getJoinToken(ctx, controlPlaneIP)
	if err != nil {
		return fmt.Errorf("failed to retrieve join token: %v", err)
	}
	// the number we want is the next one
	for i := 0; i < controlPlaneCount-len(controlPlaneNodes); i++ {
		highest++
		// what if there were none?
		var initCluster bool
		if len(controlPlaneNodes) == 0 && i == 0 {
			initCluster = true
		}
		cloudConfig := generateCloudConfig("server", initCluster, controlPlaneIP, joinToken)
		if _, err := client.InstanceCreate(ctx, oxide.InstanceCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.InstanceCreate{
				Name:   oxide.Name(fmt.Sprintf("%s%d", controlPlanePrefix, highest)),
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
		}); err != nil {
			return fmt.Errorf("failed to create control plane node: %w", err)
		}
	}

	return nil
}

// handleAddNode creates a new worker node
func handleAddNode(w http.ResponseWriter, r *http.Request) {
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
	controlPlaneIP, err := getControlPlaneIP(ctx, client, projectID)
	if err != nil {
		log.Printf("Failed to retrieve control plane IP: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}

	joinToken, err := getJoinToken(ctx, controlPlaneIP)
	if err != nil {
		log.Printf("Failed to retrieve join token: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	workerName := fmt.Sprintf("worker-%d", time.Now().Unix())
	cloudConfig := generateCloudConfig("agent", false, controlPlaneIP, joinToken)
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

func initializeSetup() error {
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

	if err := ensureClusterExists(ctx, client, projectID); err != nil {
		return fmt.Errorf("cluster verification failed: %v", err)
	}

	return nil
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "node-manager",
		Short: "Node Management Service",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Starting Node Management Service...")
			if err := initializeSetup(); err != nil {
				return fmt.Errorf("failed to initialize setup: %v", err)
			}

			// Define API routes
			http.HandleFunc("/nodes/add", handleAddNode)

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
	rootCmd.Flags().StringVar(&controlPlaneImageName, "control-plane-image-name", "debian:12-cloud", "Image to use for control plane instances")
	rootCmd.Flags().StringVar(&controlPlaneImageSource, "control-plane-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for control plane instances")
	rootCmd.Flags().StringVar(&workerImageName, "worker-image", "debian:12-cloud", "Image to use for worker nodes")
	rootCmd.Flags().StringVar(&workerImageSource, "worker-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for worker instances")
	rootCmd.Flags().Uint64Var(&controlPlaneMemory, "control-plane-memory", 4, "Memory to allocate to each control plane node, in GB")
	rootCmd.Flags().Uint64Var(&workerMemory, "worker-memory", 16, "Memory to allocate to each worker node, in GB")
	rootCmd.Flags().Uint16Var(&controlPlaneCPU, "control-plane-cpu", 2, "vCPU count to allocate to each control plane node")
	rootCmd.Flags().Uint16Var(&workerCPU, "worker-cpu", 4, "vCPU count to allocate to each worker node")

	// Execute CLI
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
	}
}
