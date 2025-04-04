package server

import (
	"net/http"

	"github.com/aifoundry-org/oxide-controller/pkg/cluster"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

type Server struct {
	logger         *log.Entry
	address        string
	oxideClient    *oxide.Client
	cluster        *cluster.Cluster
	secretName     string
	projectID      string
	prefix         string
	workerImage    string
	workerMemoryGB int
	workerCPUCount int
}

func New(address string, logger *log.Entry, oxideClient *oxide.Client, cluster *cluster.Cluster, secretName, projectID, prefix, workerImage string, workerMemoryGB, workerCPUCount int) *Server {
	return &Server{
		logger:         logger.WithField("component", "server"),
		address:        address,
		oxideClient:    oxideClient,
		cluster:        cluster,
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

	instances, err := s.cluster.CreateWorkerNodes(ctx, 1)
	if err != nil || len(instances) < 1 {
		s.logger.Debugf("Failed to create worker nodes: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	s.logger.Debugf("Worker node %s created successfully", instances[0].Name)
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Worker node added"))
}
