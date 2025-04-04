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
	logger         *log.Logger
	address        string
	oxideClient    *oxide.Client
	secretName     string
	projectID      string
	prefix         string
	workerImage    string
	workerMemoryGB int
	workerCPUCount int
}

func New(address string, logger *log.Logger, oxideClient *oxide.Client, secretName, projectID, prefix, workerImage string, workerMemoryGB, workerCPUCount int) *Server {
	return &Server{
		logger:         logger,
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
	s.logger.Infof("API listening on %s", s.address)
	return http.ListenAndServe(s.address, nil)
}

// handleAddNode creates a new worker node
func (s *Server) handleAddNode(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Processing request to add a worker node...")
	ctx := r.Context()

	controlPlaneIP, err := cluster.GetControlPlaneIP(ctx, s.logger, s.oxideClient, s.projectID, s.prefix)
	if err != nil {
		s.logger.Debugf("Failed to retrieve control plane IP: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// now that we have an initial node, we can get the join token
	joinToken, err := cluster.GetJoinToken(ctx, s.logger, nil, s.secretName)
	if err != nil {
		s.logger.Debugf("Failed to retrieve join token: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	pubkey, err := cluster.GetUserSSHPublicKey(ctx, s.logger, nil, s.secretName)
	if err != nil {
		s.logger.Debugf("Failed to retrieve SSH public key: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	workerName := fmt.Sprintf("worker-%d", time.Now().Unix())
	cloudConfig, err := cluster.GenerateCloudConfig("agent", false, controlPlaneIP.Ip, joinToken, []string{string(pubkey)})
	if err != nil {
		s.logger.Debugf("Failed to generate cloud config: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.logger.Printf("Creating worker node: %s", workerName)
	if _, err := cluster.CreateInstance(ctx, s.oxideClient, s.projectID, workerName, s.workerImage, s.workerMemoryGB, s.workerCPUCount, cloudConfig); err != nil {
		s.logger.Debugf("Failed to create worker node: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if err != nil {
		s.logger.Debugf("Failed to create worker node: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	s.logger.Debugf("Worker node %s created successfully", workerName)
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Worker node added"))
}
