package main

import (
	"context"
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
)

var (
	oxideAPIURL        string
	tokenFilePath      string
	k3sControlPlane    string
	clusterProject     string
	controlPlanePrefix string
	controlPlaneCount  int
	controlPlaneImage  string
	workerImage        string
	controlPlaneMemory uint64
	workerMemory       uint64
	controlPlaneCPU    uint16
	workerCPU          uint16
)

// Node represents a Kubernetes node
type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
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
func generateCloudConfig(nodeType string, controlPlaneIP string, joinToken string) string {
	return fmt.Sprintf(`
#cloud-config
runcmd:
  - curl -sfL https://get.k3s.io | K3S_URL="https://%s:6443" K3S_TOKEN="%s" sh -s - %s
`, controlPlaneIP, joinToken, nodeType)
}

// ensureClusterExists checks if a k3s cluster exists, and creates one if needed
func ensureClusterExists(ctx context.Context, client *oxide.Client) error {
	projects, err := client.ProjectList(ctx, oxide.ProjectListParams{})
	if err != nil {
		return fmt.Errorf("failed to list projects: %w", err)
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
			return fmt.Errorf("failed to create project: %w", err)
		}
		projectID = newProject.Id
		log.Printf("Created project '%s' with ID '%s'", clusterProject, projectID)
	}

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
	var highest int
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
		return fmt.Errorf("Failed to retrieve join token: %v", err)
	}
	// the number we want is the next one
	for i := 0; i < controlPlaneCount-len(controlPlaneNodes); i++ {
		highest++
		cloudConfig := generateCloudConfig("server", controlPlaneIP, joinToken)
		if _, err := client.InstanceCreate(ctx, oxide.InstanceCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.InstanceCreate{
				Name:     oxide.Name(fmt.Sprintf("%s%d", controlPlanePrefix, highest)),
				Memory:   oxide.ByteCount(controlPlaneMemory * GB),
				Ncpus:    oxide.InstanceCpuCount(controlPlaneCPU),
				Image:    controlPlaneImage,
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

	if err := ensureClusterExists(ctx, client); err != nil {
		log.Printf("Cluster verification failed: %v", err)
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
	cloudConfig := generateCloudConfig("agent", controlPlaneIP, joinToken)
	log.Printf("Creating worker node: %s", workerName)
	_, err = client.InstanceCreate(ctx, oxide.InstanceCreateParams{
		Project: oxide.NameOrId(clusterProject),
		Body: &oxide.InstanceCreate{
			Name:     oxide.Name(workerName),
			Memory:   oxide.ByteCount(workerMemory),
			Ncpus:    oxide.InstanceCpuCount(workerCPU),
			Image:    workerImage,
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

func main() {
	var rootCmd = &cobra.Command{
		Use:   "node-manager",
		Short: "Node Management Service",
		Run: func(cmd *cobra.Command, args []string) {
			log.Println("Starting Node Management Service...")

			// Define API routes
			http.HandleFunc("/nodes/add", handleAddNode)

			// Start HTTP server
			log.Println("API listening on port 8080")
			http.ListenAndServe(":8080", nil)
		},
	}

	// Define CLI flags
	rootCmd.Flags().StringVar(&oxideAPIURL, "oxide-api-url", "https://oxide-api.example.com", "Oxide API base URL")
	rootCmd.Flags().StringVar(&tokenFilePath, "token-file", "/data/oxide_token", "Path to Oxide API token file")
	rootCmd.Flags().StringVar(&clusterProject, "cluster-project", "ainekko-cluster", "Oxide project name for Kubernetes cluster")
	rootCmd.Flags().StringVar(&controlPlanePrefix, "control-plane-prefix", "ainekko-control-plane-", "Prefix for control plane instances")
	rootCmd.Flags().IntVar(&controlPlaneCount, "control-plane-count", 3, "Number of control plane instances to maintain")
	rootCmd.Flags().StringVar(&controlPlaneImage, "control-plane-image", "ainekko-image", "Image to use for control plane instances")
	rootCmd.Flags().StringVar(&workerImage, "worker-image", "ainekko-image", "Image to use for worker nodes")
	rootCmd.Flags().Uint64Var(&controlPlaneMemory, "control-plane-memory", 4, "Memory to allocate to each control plane node, in GB")
	rootCmd.Flags().Uint64Var(&workerMemory, "worker-memory", 16, "Memory to allocate to each worker node, in GB")
	rootCmd.Flags().Uint16Var(&controlPlaneCPU, "control-plane-cpu", 2, "vCPU count to allocate to each control plane node")
	rootCmd.Flags().Uint16Var(&workerCPU, "worker-cpu", 4, "vCPU count to allocate to each worker node")

	// Execute CLI
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
	}
}
