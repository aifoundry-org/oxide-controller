package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/aifoundry-org/oxide-controller/pkg/cluster"
	"github.com/aifoundry-org/oxide-controller/pkg/server"
	"github.com/aifoundry-org/oxide-controller/pkg/util"
)

const (
	defaultBlocksize = 512
)

func rootCmd() (*cobra.Command, error) {
	var (
		oxideAPIURL                string
		tokenFilePath              string
		clusterProject             string
		controlPlanePrefix         string
		workerPrefix               string
		controlPlaneCount          uint
		workerCount                uint
		controlPlaneImageName      string
		controlPlaneImageSource    string
		workerImageName            string
		workerImageSource          string
		controlPlaneMemory         uint64
		workerMemory               uint64
		controlPlaneCPU            uint16
		workerCPU                  uint16
		clusterInitWait            int
		userSSHPublicKey           string
		kubeconfigPath             string
		kubeconfigOverwrite        bool
		controlPlaneSecret         string
		verbose                    int
		address                    string
		workerExternalIP           bool
		controlPlaneExternalIP     bool
		controlLoopMins            int
		runOnce                    bool
		controlPlaneImageBlocksize int
		workerImageBlocksize       int
		imageParallelism           int

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
			var (
				pubkey []byte
				err    error
			)
			if userSSHPublicKey != "" {
				logentry.Debugf("Loading SSH key from %s", userSSHPublicKey)
				pubkey, err = util.LoadFile(userSSHPublicKey)
				if err != nil {
					return fmt.Errorf("failed to load ssh public key at %s: %w", userSSHPublicKey, err)
				}
			}

			logentry.Debugf("Loading kubeconfig from %s", kubeconfigPath)
			kubeconfig, err := util.LoadFile(kubeconfigPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
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
				controlPlanePrefix, workerPrefix, int(controlPlaneCount), int(workerCount),
				cluster.NodeSpec{Image: cluster.Image{Name: controlPlaneImageName, Source: controlPlaneImageSource, Blocksize: controlPlaneImageBlocksize}, MemoryGB: int(controlPlaneMemory), CPUCount: int(controlPlaneCPU), ExternalIP: controlPlaneExternalIP},
				cluster.NodeSpec{Image: cluster.Image{Name: workerImageName, Source: workerImageSource, Blocksize: workerImageBlocksize}, MemoryGB: int(workerMemory), CPUCount: int(workerCPU), ExternalIP: workerExternalIP},
				imageParallelism,
				controlPlaneSecret, kubeconfig, pubkey,
				time.Duration(clusterInitWait)*time.Minute,
				kubeconfigOverwrite,
			)
			// we perform 2 execution loops of the cluster execute function:
			// - the first one is to create the cluster and get the kubeconfig
			// - the second one is to ensure the cluster is up and running
			newKubeconfig, err := c.Execute(ctx)
			if err != nil {
				return fmt.Errorf("failed to initialize setup: %v", err)
			}
			if len(newKubeconfig) > 0 && (len(kubeconfig) == 0 || kubeconfigOverwrite) {
				logentry.Infof("Saving new kubeconfig to %s", kubeconfigPath)
				if err := util.SaveFile(kubeconfigPath, newKubeconfig, kubeconfigOverwrite); err != nil {
					return fmt.Errorf("failed to save kubeconfig: %w", err)
				}
			}

			if runOnce {
				logentry.Infof("Run once mode enabled, exiting after first run")
				return nil
			}

			// start each control loop
			var (
				wg    sync.WaitGroup
				errCh = make(chan error, 2) // buffered channel to hold up to 3 errors
			)

			// 1- cluster manager
			wg.Add(1)
			go func() {
				defer wg.Done()
				controlLoopSleep := time.Duration(controlLoopMins) * time.Minute
				for {
					logentry.Debugf("Running control loop")
					// we do not overwrite the kubeconfig file on future loops
					if _, err := c.Execute(ctx); err != nil {
						errCh <- fmt.Errorf("failed to run control loop: %v", err)
					}
					logentry.Debugf("Control loop complete, sleeping %s", controlLoopSleep)
					time.Sleep(controlLoopSleep)
				}
			}()

			// 2- API server
			wg.Add(1)
			go func() {
				// serve REST endpoints
				defer wg.Done()
				logentry.Infof("Starting server on address %s", address)
				s := server.New(address, logentry, oxideClient, c, controlPlaneSecret, clusterProject, controlPlanePrefix, workerImageName, int(workerMemory), int(workerCPU))
				errCh <- s.Serve()
			}()

			wg.Wait()
			close(errCh)
			var errs []error
			for err := range errCh {
				errs = append(errs, err)
			}
			log.Info("all loops have finished")
			return errors.Join(errs...)
		},
	}

	// Define CLI flags
	cmd.Flags().StringVar(&oxideAPIURL, "oxide-api-url", "https://oxide-api.example.com", "Oxide API base URL")
	cmd.Flags().StringVar(&tokenFilePath, "token-file", "/data/oxide_token", "Path to Oxide API token file")
	cmd.Flags().StringVar(&clusterProject, "cluster-project", "ainekko-cluster", "Oxide project name for Kubernetes cluster")
	cmd.Flags().StringVar(&controlPlanePrefix, "control-plane-prefix", "ainekko-control-plane-", "Prefix for control plane instances")
	cmd.Flags().StringVar(&workerPrefix, "worker-prefix", "ainekko-worker-", "Prefix for worker instances")
	cmd.Flags().UintVar(&controlPlaneCount, "control-plane-count", 3, "Number of control plane instances to maintain")
	cmd.Flags().StringVar(&controlPlaneImageName, "control-plane-image-name", "debian-12-cloud", "Image to use for control plane instances")
	cmd.Flags().StringVar(&controlPlaneImageSource, "control-plane-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for control plane instances")
	cmd.Flags().IntVar(&controlPlaneImageBlocksize, "control-plane-image-blocksize", defaultBlocksize, "Blocksize to use for control plane images")
	cmd.Flags().StringVar(&workerImageName, "worker-image-name", "debian-12-cloud", "Image to use for worker nodes")
	cmd.Flags().IntVar(&workerImageBlocksize, "worker-image-blocksize", defaultBlocksize, "Blocksize to use for worker images")
	cmd.Flags().StringVar(&workerImageSource, "worker-image-source", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for worker instances")
	cmd.Flags().Uint64Var(&controlPlaneMemory, "control-plane-memory", 4, "Memory to allocate to each control plane node, in GB")
	cmd.Flags().UintVar(&workerCount, "worker-count", 0, "Number of worker instances to create on startup and maintain, until changed via API")
	cmd.Flags().Uint64Var(&workerMemory, "worker-memory", 16, "Memory to allocate to each worker node, in GB")
	cmd.Flags().Uint16Var(&controlPlaneCPU, "control-plane-cpu", 2, "vCPU count to allocate to each control plane node")
	cmd.Flags().Uint16Var(&workerCPU, "worker-cpu", 4, "vCPU count to allocate to each worker node")
	cmd.Flags().IntVar(&clusterInitWait, "cluster-init-wait", 5, "Time to wait for the first control plane node to be up and running (in minutes)")
	cmd.Flags().StringVar(&userSSHPublicKey, "user-ssh-public-key", "", "Path to public key to inject in all deployed cloud instances")
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "~/.kube/oxide-controller-config", "Path to save kubeconfig when generating new cluster, or to use for accessing existing cluster")
	cmd.Flags().BoolVar(&kubeconfigOverwrite, "kubeconfig-overwrite", false, "Whether or not to override the kubeconfig file if it already exists and a new cluster is created")
	cmd.Flags().StringVar(&controlPlaneSecret, "control-plane-secret", "kube-system/oxide-controller-secret", "secret in Kubernetes cluster where the following are stored: join token, user ssh public key, controller ssh private/public keypair; should be as <namespace>/<name>")
	cmd.Flags().BoolVar(&workerExternalIP, "worker-external-ip", false, "Whether or not to assign an ephemeral public IP to the worker nodes, useful for debugging")
	cmd.Flags().BoolVar(&controlPlaneExternalIP, "control-plane-external-ip", true, "Whether or not to assign an ephemeral public IP to the control plane nodes, needed to access cluster from outside sled, as well as for debugging")
	cmd.Flags().IntVarP(&verbose, "verbose", "v", 0, "set log level, 0 is info, 1 is debug, 2 is trace")
	cmd.Flags().StringVar(&address, "address", ":8080", "Address to bind the server to")
	cmd.Flags().BoolVar(&runOnce, "runonce", false, "Run the server once and then exit, do not run a long-running control loop for checking the controller or listening for API calls")
	cmd.Flags().IntVar(&controlLoopMins, "control-loop-mins", 5, "How often to run the control loop, in minutes")
	cmd.Flags().IntVar(&imageParallelism, "image-parallelism", 1, "How many parallel threads to use for uploading images to the sled")

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
