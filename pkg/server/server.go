package server

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/aifoundry-org/oxide-controller/pkg/cluster"
	"github.com/gorilla/mux"

	log "github.com/sirupsen/logrus"
)

type Server struct {
	logger         *log.Entry
	address        string
	cluster        *cluster.Cluster
	secretName     string
	projectID      string
	prefix         string
	workerImage    string
	workerMemoryGB int
	workerCPUCount int
}

func New(address string, logger *log.Entry, cluster *cluster.Cluster, secretName, projectID, prefix, workerImage string, workerMemoryGB, workerCPUCount int) *Server {
	return &Server{
		logger:         logger.WithField("component", "server"),
		address:        address,
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
	r := mux.NewRouter()
	r.HandleFunc("/nodes/workers/modify", s.handleModifyNode).Methods("POST")
	r.HandleFunc("/nodes/workers", s.handleGetNodeCount).Methods("GET")

	// Start HTTP server
	s.logger.Infof("API listening on %s", s.address)
	return http.ListenAndServe(s.address, r)
}

// handleModifyNode modifies worker node count
func (s *Server) handleModifyNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.logger.Debug("Processing request to modify worker node count...")
	countB, err := io.ReadAll(r.Body)
	if err != nil {
		s.logger.Debugf("Failed to read request body: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	count, err := strconv.Atoi(string(countB))
	if err != nil {
		s.logger.Debugf("Failed to convert request body to int: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if count < 0 {
		s.logger.Debug("Worker node count cannot be negative")
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if count > int(^uint32(0)) {
		s.logger.Debug("Worker node count is too large")
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	existingCount, err := s.cluster.GetWorkerCount(ctx)
	if err != nil {
		s.logger.Debugf("Failed to get existing worker node count: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := s.cluster.SetWorkerCount(ctx, count); err != nil {
		s.logger.Debugf("Failed to change worker node count: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	s.logger.Debugf("Worker node count modified modified successfully to: %d", count)
	w.WriteHeader(http.StatusCreated)
	w.Write(fmt.Appendf(nil, "Worker count changed from %d to %d", existingCount, count))
}

// handleGetNodeCount gets worker node count
func (s *Server) handleGetNodeCount(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Processing request to get worker node count...")

	count, err := s.cluster.GetWorkerNodeCount()
	if err != nil {
		s.logger.Debugf("Failed to get worker node count: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	s.logger.Debugf("Worker node count retrieved: %d", count)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(strconv.Itoa(int(count))))
}
