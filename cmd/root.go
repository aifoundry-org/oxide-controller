package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/aifoundry-org/oxide-controller/pkg/cluster"
	"github.com/aifoundry-org/oxide-controller/pkg/server"
	"github.com/aifoundry-org/oxide-controller/pkg/util"
)

func rootCmd() (*cobra.Command, error) {
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
		verbose                 int
		address                 string

		logger = log.New()
	)

	cmd := &cobra.Command{
		Use:   "oxide-controller",
		Short: "Oxide Kubernetes Management Service",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			switch verbose {
			case 0:
				logger.SetLevel(log.InfoLevel)
			case 1:
				logger.SetLevel(log.DebugLevel)
			case 2:
				logger.SetLevel(log.TraceLevel)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			logentry := logger.WithField("service", "oxide-controller")
			logentry.Infof("Starting Node Management Service...")

			// load the ssh key provided, if any
			// loadSSHKey returns empty key material and no error if the userSSHPublicKey is empty
			logentry.Debugf("Loading SSH key from %s", userSSHPublicKey)
			pubkey, err := util.LoadFile(userSSHPublicKey)
			if err != nil {
				return fmt.Errorf("failed to load ssh public key at %s: %w", userSSHPublicKey, err)
			}

			logentry.Debugf("Loading kubeconfig from %s", kubeconfigPath)
			kubeconfig, err := util.LoadFileAllowMissing(kubeconfigPath)
			if err != nil {
				return fmt.Errorf("failed to load kubeconfig at %s: %w", kubeconfigPath, err)
			}

			logentry.Debugf("Loading Oxide token from %s", tokenFilePath)
			b, err := util.LoadFile(tokenFilePath)
			oxideToken := strings.TrimSuffix(string(b), "\n")
			if err != nil {
				return fmt.Errorf("failed to load oxide token at %s: %w", tokenFilePath, err)
			}

			cfg := oxide.Config{
				Host:  oxideAPIURL,
				Token: string(oxideToken),
			}
			logentry.Debugf("Creating Oxide API client with config: %+v", cfg)
			oxideClient, err := oxide.NewClient(&cfg)
			if err != nil {
				return fmt.Errorf("failed to create Oxide API client: %v", err)
			}

			ctx := context.Background()

			c := cluster.New(logentry, oxideClient, clusterProject,
				controlPlanePrefix, controlPlaneCount,
				cluster.NodeSpec{Image: cluster.Image{Name: controlPlaneImageName, Source: controlPlaneImageSource}, MemoryGB: int(controlPlaneMemory), CPUCount: int(controlPlaneCPU)},
				cluster.NodeSpec{Image: cluster.Image{Name: workerImageName, Source: workerImageSource}, MemoryGB: int(workerMemory), CPUCount: int(workerCPU)},
				controlPlaneSecret, kubeconfig, pubkey,
			)
			logentry.Debugf("Ensuring project exists: %s", clusterProject)
			newKubeconfig, err := c.Initialize(ctx, clusterInitWait)
			if err != nil {
				return fmt.Errorf("failed to initialize setup: %v", err)
			}
			if len(kubeconfig) == 0 && len(newKubeconfig) > 0 {
				logentry.Infof("Saving new kubeconfig to %s", kubeconfigPath)
				if err := util.SaveFileIfNotExists(kubeconfigPath, newKubeconfig); err != nil {
					return fmt.Errorf("failed to save kubeconfig: %w", err)
				}
			}

			// serve REST endpoints
			logentry.Infof("Starting server on address %s", address)
			s := server.New(address, logentry, oxideClient, c, controlPlaneSecret, clusterProject, controlPlanePrefix, workerImageName, int(workerMemory), int(workerCPU))
			return s.Serve()
		},
	}

	// Define CLI flags
	cmd.Flags().StringVar(&oxideAPIURL, "oxide-api-url", "https://oxide-api.example.com", "Oxide API base URL")
	cmd.Flags().StringVar(&tokenFilePath, "token-file", "/data/oxide_token", "Path to Oxide API token file")
	cmd.Flags().StringVar(&clusterProject, "cluster-project", "ainekko-cluster", "Oxide project name for Kubernetes cluster")
	cmd.Flags().StringVar(&controlPlanePrefix, "control-plane-prefix", "ainekko-control-plane-", "Prefix for control plane instances")
	cmd.Flags().IntVar(&controlPlaneCount, "control-plane-count", 3, "Number of control plane instances to maintain")
	cmd.Flags().StringVar(&controlPlaneImageName, "control-plane-image-name", "debian-12-cloud", "Image to use for control plane instances")
	cmd.Flags().StringVar(&controlPlaneImageSource, "control-plane-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for control plane instances")
	cmd.Flags().StringVar(&workerImageName, "worker-image", "debian-12-cloud", "Image to use for worker nodes")
	cmd.Flags().StringVar(&workerImageSource, "worker-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for worker instances")
	cmd.Flags().Uint64Var(&controlPlaneMemory, "control-plane-memory", 4, "Memory to allocate to each control plane node, in GB")
	cmd.Flags().Uint64Var(&workerMemory, "worker-memory", 16, "Memory to allocate to each worker node, in GB")
	cmd.Flags().Uint16Var(&controlPlaneCPU, "control-plane-cpu", 2, "vCPU count to allocate to each control plane node")
	cmd.Flags().Uint16Var(&workerCPU, "worker-cpu", 4, "vCPU count to allocate to each worker node")
	cmd.Flags().IntVar(&clusterInitWait, "cluster-init-wait", 5, "Time to wait for the first control plane node to be up and running (in minutes)")
	cmd.Flags().StringVar(&userSSHPublicKey, "user-ssh-public-key", "", "Path to public key to inject in all deployed cloud instances")
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "~/.kube/oxide-controller-config", "Path to save kubeconfig when generating new cluster, or to use for accessing existing cluster")
	cmd.Flags().StringVar(&controlPlaneSecret, "control-plane-secret", "kube-system/oxide-controller-secret", "secret in Kubernetes cluster where the following are stored: join token, user ssh public key, controller ssh private/public keypair; should be as <namespace>/<name>")
	cmd.Flags().IntVarP(&verbose, "verbose", "v", 0, "set log level, 0 is info, 1 is debug, 2 is trace")
	cmd.Flags().StringVar(&address, "address", ":8080", "Address to bind the server to")

	return cmd, nil
}

// Execute primary function for cobra
func Execute() {
	rootCmd, err := rootCmd()
	if err != nil {
		log.Fatal(err)
	}
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
