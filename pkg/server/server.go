package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/aifoundry-org/oxide-controller/pkg/cluster"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

type Server struct {
	address        string
	oxideClient    *oxide.Client
	secretName     string
	projectID      string
	prefix         string
	workerImage    string
	workerMemoryGB int
	workerCPUCount int
}

func New(address string, oxideClient *oxide.Client, secretName, projectID, prefix, workerImage string, workerMemoryGB, workerCPUCount int) *Server {
	return &Server{
		address:        address,
		oxideClient:    oxideClient,
		secretName:     secretName,
		projectID:      projectID,
		prefix:         prefix,
		workerImage:    workerImage,
		workerMemoryGB: workerMemoryGB,
		workerCPUCount: workerCPUCount,
	}
}

func (s *Server) Serve() error {
	// Define API routes
	http.HandleFunc("/nodes/add", s.handleAddNode)

	// Start HTTP server
	log.Println("API listening on port 8080")
	return http.ListenAndServe(s.address, nil)
}

// handleAddNode creates a new worker node
func (s *Server) handleAddNode(w http.ResponseWriter, r *http.Request) {
	log.Println("Processing request to add a worker node...")
	ctx := r.Context()

	controlPlaneIP, err := cluster.GetControlPlaneIP(ctx, s.oxideClient, s.projectID, s.prefix)
	if err != nil {
		log.Printf("Failed to retrieve control plane IP: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// now that we have an initial node, we can get the join token
	joinToken, err := cluster.GetJoinToken(ctx, nil, s.secretName)
	if err != nil {
		log.Printf("Failed to retrieve join token: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	pubkey, err := cluster.GetUserSSHPublicKey(ctx, nil, s.secretName)
	if err != nil {
		log.Printf("Failed to retrieve SSH public key: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	workerName := fmt.Sprintf("worker-%d", time.Now().Unix())
	cloudConfig, err := cluster.GenerateCloudConfig("agent", false, controlPlaneIP.Ip, joinToken, []string{string(pubkey)})
	if err != nil {
		log.Printf("Failed to generate cloud config: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	log.Printf("Creating worker node: %s", workerName)
	if _, err := cluster.CreateInstance(ctx, s.oxideClient, s.projectID, workerName, s.workerImage, s.workerMemoryGB, s.workerCPUCount, cloudConfig); err != nil {
		log.Printf("Failed to create worker node: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if err != nil {
		log.Printf("Failed to create worker node: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Worker node added"))
}
