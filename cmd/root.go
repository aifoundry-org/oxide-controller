package main

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
	"github.com/aifoundry-org/oxide-controller/pkg/config"
	logpkg "github.com/aifoundry-org/oxide-controller/pkg/log"
	oxidepkg "github.com/aifoundry-org/oxide-controller/pkg/oxide"
	"github.com/aifoundry-org/oxide-controller/pkg/server"
	"github.com/aifoundry-org/oxide-controller/pkg/util"
)

const (
	defaultBlocksize  = 512
	ociImageDevPrefix = "dev:"

	flagOxideAPIURL                = "oxide-api-url"
	flagOxideAPIToken              = "oxide-token"
	flagOxideProfile               = "oxide-profile"
	flagClusterProject             = "cluster-project"
	flagControlPlanePrefix         = "control-plane-prefix"
	flagWorkerPrefix               = "worker-prefix"
	flagControlPlaneCount          = "control-plane-count"
	flagControlPlaneImageName      = "control-plane-image-name"
	flagControlPlaneImageSource    = "control-plane-image-source"
	flagControlPlaneImageBlocksize = "control-plane-image-blocksize"
	flagWorkerImageName            = "worker-image-name"
	flagWorkerImageBlocksize       = "worker-image-blocksize"
	flagWorkerImageSource          = "worker-image-source"
	flagWorkerRootDiskSize         = "worker-root-disk-size"
	flagControlPlaneRootDiskSize   = "control-plane-root-disk-size"
	flagWorkerExtraDiskSize        = "worker-extra-disk-size"
	flagControlPlaneExtraDiskSize  = "control-plane-extra-disk-size"
	flagControlPlaneMemory         = "control-plane-memory"
	flagWorkerCount                = "worker-count"
	flagWorkerMemory               = "worker-memory"
	flagControlPlaneCPU            = "control-plane-cpu"
	flagWorkerCPU                  = "worker-cpu"
	flagClusterInitWait            = "cluster-init-wait"
	flagUserSSHPublicKey           = "user-ssh-public-key"
	flagKubeconfig                 = "kubeconfig"
	flagKubeconfigOverwrite        = "kubeconfig-overwrite"
	flagControlPlaneSecret         = "control-plane-secret"
	flagControlPlaneNamespace      = "control-plane-namespace"
	flagWorkerExternalIP           = "worker-external-ip"
	flagControlPlaneExternalIP     = "control-plane-external-ip"
	flagVerbose                    = "verbose"
	flagAddress                    = "address"
	flagNoPivot                    = "no-pivot"
	flagControllerOCIImage         = "controller-oci-image"
	flagControlLoopMins            = "control-loop-mins"
	flagImageParallelism           = "image-parallelism"
	flagTailscaleAuthKey           = "tailscale-auth-key"
	flagTailscaleAPIKey            = "tailscale-api-key"
	flagTailscaleTag               = "tailscale-tag"
	flagTailscaleTailnet           = "tailscale-tailnet"
)

func rootCmd() (*cobra.Command, error) {
	var (
		oxideAPIURL                 string
		oxideToken                  string
		oxideProfile                string
		clusterProject              string
		controlPlanePrefix          string
		workerPrefix                string
		controlPlaneCount           uint
		workerCount                 uint
		controlPlaneImageName       string
		controlPlaneImageSource     string
		workerImageName             string
		workerImageSource           string
		controlPlaneMemory          uint64
		workerMemory                uint64
		controlPlaneCPU             uint16
		workerCPU                   uint16
		clusterInitWait             int
		userSSHPublicKey            string
		kubeconfigPath              string
		kubeconfigOverwrite         bool
		controlPlaneSecret          string
		controlPlaneNamespace       string
		verbose                     int
		address                     string
		workerExternalIP            bool
		controlPlaneExternalIP      bool
		controlLoopMins             int
		noPivot                     bool
		controlPlaneImageBlocksize  int
		workerImageBlocksize        int
		imageParallelism            int
		workerExtraDiskSizeGB       uint
		controlPlaneExtraDiskSizeGB uint
		workerRootDiskSizeGB        uint
		controlPlaneRootDiskSizeGB  uint
		tailscaleAuthKey            string
		tailscaleAPIKey             string
		tailscaleTag                string
		tailscaleTailnet            string
		controllerOCIImage          string

		logger = log.New()
	)

	cmd := &cobra.Command{
		Use:   "oxide-controller",
		Short: "Oxide Kubernetes Management Service",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			logger.SetLevel(logpkg.GetLevel(verbose))
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
			ctx := context.Background()

			logentry.Debugf("Loading kubeconfig from %s", kubeconfigPath)
			kubeconfig, err := util.LoadFile(kubeconfigPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to load kubeconfig at %s: %w", kubeconfigPath, err)
			}

			// with the kubeconfig, or not, we can get the *rest.Config and determine if we are in-cluster
			restConfig, err := cluster.GetRestConfig(kubeconfig)
			if err != nil {
				return fmt.Errorf("failed to get kubeconfig: %w", err)
			}
			if restConfig == nil {
				logentry.Debug("No valid kubeconfig found at startup")
			} else {
				logentry.Debugf("Cluster authentication source: %v", restConfig.Source)
			}

			// with the restConfig, we can retrieve the entire config from the cluster.
			// we only will override it if there was an explicit cmdline override.
			// If we cannot retrieve the config, that is ok as well
			var controllerConfig *config.ControllerConfig
			if restConfig != nil {
				controllerConfig, err = cluster.GetSecretConfig(ctx, restConfig.Config, logentry, controlPlaneNamespace, controlPlaneSecret)
				// do we care about the error here?
				// - in-cluster, that is an error
				if err != nil {
					if restConfig.Source == cluster.ConfigSourceInCluster {
						return fmt.Errorf("failed to get controller config from cluster while within cluster: %w", err)
					} else {
						logentry.Debugf("Failed to get controller config from cluster, not fatal, continuing: %v", err)
					}
				}
			}

			// scenarios for oxide:
			// 1- oxideAPIURL and oxideToken are provided, use them
			// 2- oxideProfile is provided, use it to get oxideAPIURL and oxideToken
			// 3- oxideProfile is not provided, use the default profile, succeed, use them
			// 4- oxideProfile is not provided, did not succeed, look for it in-cluster
			switch {
			case oxideProfile != "" && (oxideAPIURL != "" || oxideToken != ""):
				// cannot provide both --oxide-profile and --oxide-token/--oxide-api-url
				return fmt.Errorf("cannot provide both --oxide-profile and --oxide-token/--oxide-api-url")
			case oxideAPIURL != "" && oxideToken != "":
				// use the provided oxideAPIURL and oxideToken
				logentry.Debugf("Using oxide API URL %s and token ****", oxideAPIURL)
			case oxideProfile != "":
				// read the profile
				logentry.Debugf("Loading oxide profile %s", oxideProfile)
				profileHost, profileToken, err := oxidepkg.GetProfile(&oxide.Config{Profile: oxideProfile})
				if err != nil {
					return fmt.Errorf("failed to load oxide profile %s: %w", oxideProfile, err)
				}
				oxideAPIURL = profileHost
				oxideToken = profileToken
			case restConfig != nil && restConfig.Source == cluster.ConfigSourceInCluster && controllerConfig != nil && controllerConfig.OxideURL != "" && controllerConfig.OxideToken != "":
				// we are in-cluster, try to load the oxide profile from the secret
				logentry.Debugf("Loading oxide profile from in-cluster secret")
				oxideAPIURL = controllerConfig.OxideURL
				oxideToken = controllerConfig.OxideToken
			default:
				// nothing provided, try to load the default
				logentry.Debugf("Loading oxide default profile")
				profileHost, profileToken, err := oxidepkg.GetProfile(&oxide.Config{UseDefaultProfile: true})
				if err != nil {
					return fmt.Errorf("failed to load oxide default profile: %w", err)
				}
				oxideAPIURL = profileHost
				oxideToken = profileToken
			}

			if strings.HasPrefix(oxideToken, "file:") {
				tokenFilePath := strings.TrimPrefix(oxideToken, "file:")
				oxideToken = ""
				logentry.Debugf("Loading oxide token from %s", tokenFilePath)
				b, err := os.ReadFile(tokenFilePath)
				if err != nil {
					return fmt.Errorf("failed to load oxide token at %s: %w", tokenFilePath, err)
				}
				oxideToken = strings.TrimSuffix(string(b), "\n")
			}

			// if tailscaleAuthKey starts with file: then it is a path
			if strings.HasPrefix(tailscaleAuthKey, "file:") {
				tailscaleAuthKeyFile := strings.TrimPrefix(tailscaleAuthKey, "file:")
				tailscaleAuthKey = ""
				logentry.Debugf("Loading tailscale auth key from %s", tailscaleAuthKeyFile)
				key, err := os.ReadFile(tailscaleAuthKeyFile)
				if err != nil {
					return fmt.Errorf("failed to load tailscale auth key at %s: %w", tailscaleAuthKeyFile, err)
				}
				tailscaleAuthKey = strings.TrimSuffix(string(key), "\n")
			}

			if strings.HasPrefix(tailscaleAPIKey, "file:") {
				tailscaleAPIKeyFile := strings.TrimPrefix(tailscaleAPIKey, "file:")
				tailscaleAPIKey = ""
				logentry.Debugf("Loading tailscale api key from %s", tailscaleAPIKeyFile)
				key, err := os.ReadFile(tailscaleAPIKeyFile)
				if err != nil {
					return fmt.Errorf("failed to load tailscale api key at %s: %w", tailscaleAPIKeyFile, err)
				}
				tailscaleAPIKey = strings.TrimSuffix(string(key), "\n")
			}

			if tailscaleAuthKey != "" {
				if tailscaleAPIKey == "" {
					return fmt.Errorf("tailscale api key must be provided if using tailscale auth key")
				}
				if tailscaleTailnet == "" {
					return fmt.Errorf("tailscale tailnet must be provided if using tailscale auth key")
				}
			}
			// load the ssh key provided, if any
			// loadSSHKey returns empty key material and no error if the userSSHPublicKey is empty
			var pubkey []byte
			if userSSHPublicKey != "" {
				logentry.Debugf("Loading SSH key from %s", userSSHPublicKey)
				pubkey, err = util.LoadFile(userSSHPublicKey)
				if err != nil {
					return fmt.Errorf("failed to load ssh public key at %s: %w", userSSHPublicKey, err)
				}
			}

			// At this point that we set cmd.SilenceUsage = true, as we no longer need to show the usage.
			// All future errors are by the system, not erroneous user input.
			cmd.SilenceUsage = true

			// we already may have a controllerConfig as retrieved from the cluster.
			// Now need to merge in any CLI overrides, but only if set explicitly.

			// If we already found a controllerConfig from inside the cluster,
			// we only override if it was set explicitly by CLI flag, not just the flag default.
			// If we did not find one, then we create a new one with the CLI flags, set or default.
			if controllerConfig != nil {
				if len(pubkey) > 0 {
					controllerConfig.UserSSHPublicKey = string(pubkey)
				}
				controllerConfig.OxideToken = oxideToken
				controllerConfig.OxideURL = oxideAPIURL
				if cmd.Flags().Changed(flagControlPlaneNamespace) {
					controllerConfig.ControlPlaneNamespace = controlPlaneNamespace
				}
				if cmd.Flags().Changed(flagControlPlaneSecret) {
					controllerConfig.SecretName = controlPlaneSecret
				}
				if cmd.Flags().Changed(flagClusterProject) {
					controllerConfig.ClusterProject = clusterProject
				}
				if cmd.Flags().Changed(flagControlPlaneCount) {
					controllerConfig.ControlPlaneCount = controlPlaneCount
				}
				if cmd.Flags().Changed(flagWorkerCount) {
					controllerConfig.WorkerCount = workerCount
				}
				if cmd.Flags().Changed(flagAddress) {
					controllerConfig.Address = address
				}
				if cmd.Flags().Changed(flagControlLoopMins) {
					controllerConfig.ControlLoopMins = controlLoopMins
				}
				if cmd.Flags().Changed(flagImageParallelism) {
					controllerConfig.ImageParallelism = imageParallelism
				}
				if cmd.Flags().Changed(flagTailscaleAPIKey) {
					controllerConfig.TailscaleAPIKey = tailscaleAPIKey
				}
				if cmd.Flags().Changed(flagTailscaleAuthKey) {
					controllerConfig.TailscaleAuthKey = tailscaleAuthKey
				}
				if cmd.Flags().Changed(flagTailscaleTag) {
					controllerConfig.TailscaleTag = tailscaleTag
					controllerConfig.ControlPlaneSpec.TailscaleTag = tailscaleTag
					controllerConfig.ControlPlaneSpec.TailscaleAuthKey = tailscaleAuthKey
					controllerConfig.WorkerSpec.TailscaleTag = tailscaleTag
					controllerConfig.WorkerSpec.TailscaleAuthKey = tailscaleAuthKey
				}
				if cmd.Flags().Changed(flagTailscaleTailnet) {
					controllerConfig.TailscaleTailnet = tailscaleTailnet
				}
				if cmd.Flags().Changed(flagControlPlaneImageName) {
					controllerConfig.ControlPlaneSpec.Image.Name = controlPlaneImageName
				}
				if cmd.Flags().Changed(flagControlPlaneImageSource) {
					controllerConfig.ControlPlaneSpec.Image.Source = controlPlaneImageSource
				}
				if cmd.Flags().Changed(flagControlPlaneImageBlocksize) {
					controllerConfig.ControlPlaneSpec.Image.Blocksize = controlPlaneImageBlocksize
				}
				if cmd.Flags().Changed(flagControlPlanePrefix) {
					controllerConfig.ControlPlaneSpec.Prefix = controlPlanePrefix
				}
				if cmd.Flags().Changed(flagControlPlaneMemory) {
					controllerConfig.ControlPlaneSpec.MemoryGB = int(controlPlaneMemory)
				}
				if cmd.Flags().Changed(flagControlPlaneCPU) {
					controllerConfig.ControlPlaneSpec.CPUCount = int(controlPlaneCPU)
				}
				if cmd.Flags().Changed(flagControlPlaneExternalIP) {
					controllerConfig.ControlPlaneSpec.ExternalIP = controlPlaneExternalIP
				}
				if cmd.Flags().Changed(flagControlPlaneRootDiskSize) {
					controllerConfig.ControlPlaneSpec.RootDiskSize = int(controlPlaneRootDiskSizeGB * cluster.GB)
				}
				if cmd.Flags().Changed(flagControlPlaneExtraDiskSize) {
					controllerConfig.ControlPlaneSpec.ExtraDiskSize = int(controlPlaneExtraDiskSizeGB * cluster.GB)
				}
				if cmd.Flags().Changed(flagWorkerImageName) {
					controllerConfig.WorkerSpec.Image.Name = workerImageName
				}
				if cmd.Flags().Changed(flagWorkerImageSource) {
					controllerConfig.WorkerSpec.Image.Source = workerImageSource
				}
				if cmd.Flags().Changed(flagWorkerImageBlocksize) {
					controllerConfig.WorkerSpec.Image.Blocksize = workerImageBlocksize
				}
				if cmd.Flags().Changed(flagWorkerPrefix) {
					controllerConfig.WorkerSpec.Prefix = workerPrefix
				}
				if cmd.Flags().Changed(flagWorkerMemory) {
					controllerConfig.WorkerSpec.MemoryGB = int(workerMemory)
				}
				if cmd.Flags().Changed(flagWorkerCPU) {
					controllerConfig.WorkerSpec.CPUCount = int(workerCPU)
				}
				if cmd.Flags().Changed(flagWorkerExternalIP) {
					controllerConfig.WorkerSpec.ExternalIP = workerExternalIP
				}
				if cmd.Flags().Changed(flagWorkerRootDiskSize) {
					controllerConfig.WorkerSpec.RootDiskSize = int(workerRootDiskSizeGB * cluster.GB)
				}
				if cmd.Flags().Changed(flagWorkerExtraDiskSize) {
					controllerConfig.WorkerSpec.ExtraDiskSize = int(workerExtraDiskSizeGB * cluster.GB)
				}
			} else {
				logentry.Debugf("No controller config found in cluster, creating a new one")
				controllerConfig = &config.ControllerConfig{
					UserSSHPublicKey:  string(pubkey),
					OxideToken:        oxideToken,
					OxideURL:          oxideAPIURL,
					ClusterProject:    clusterProject,
					ControlPlaneCount: controlPlaneCount,
					ControlPlaneSpec: config.NodeSpec{
						Image:            config.Image{Name: controlPlaneImageName, Source: controlPlaneImageSource, Blocksize: controlPlaneImageBlocksize},
						Prefix:           controlPlanePrefix,
						MemoryGB:         int(controlPlaneMemory),
						CPUCount:         int(controlPlaneCPU),
						ExternalIP:       controlPlaneExternalIP,
						RootDiskSize:     int(controlPlaneRootDiskSizeGB * cluster.GB),
						ExtraDiskSize:    int(controlPlaneExtraDiskSizeGB * cluster.GB),
						TailscaleAuthKey: tailscaleAuthKey,
						TailscaleTag:     tailscaleTag,
					},
					WorkerCount: workerCount,
					WorkerSpec: config.NodeSpec{
						Image:            config.Image{Name: workerImageName, Source: workerImageSource, Blocksize: workerImageBlocksize},
						Prefix:           workerPrefix,
						MemoryGB:         int(workerMemory),
						CPUCount:         int(workerCPU),
						ExternalIP:       workerExternalIP,
						RootDiskSize:     int(workerRootDiskSizeGB * cluster.GB),
						ExtraDiskSize:    int(workerExtraDiskSizeGB * cluster.GB),
						TailscaleAuthKey: tailscaleAuthKey,
						TailscaleTag:     tailscaleTag,
					},

					ControlPlaneNamespace: controlPlaneNamespace,
					SecretName:            controlPlaneSecret,
					Address:               address,
					ControlLoopMins:       controlLoopMins,
					ImageParallelism:      imageParallelism,
					TailscaleAuthKey:      tailscaleAuthKey,
					TailscaleAPIKey:       tailscaleAPIKey,
					TailscaleTag:          tailscaleTag,
					TailscaleTailnet:      tailscaleTailnet,
				}
			}

			c := cluster.New(
				logentry,
				controllerConfig,
				kubeconfigOverwrite,
				controllerOCIImage,
				time.Duration(clusterInitWait)*time.Minute,
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

			// Several possibilities:
			// 1- we are running in the cluster, in which case we should just keep running
			// 2- we are running locally, in which case we should load the helm charts onto the cluster and exit
			// 3- we are running locally, and we have nopivot, in which case we should just keep running
			//
			// load the helm charts onto the cluster and pivot the controller to the cluster,
			// then shut down this server.
			//
			// Unless nopivot is true, in which case we do not pivot
			// and just keep running locally.
			// if we are running in dev mode, and we will want to pivot, we will need to build a suitable copy
			// of the controller binary to load into the cluster.

			if !noPivot {
				// if we are in dev mode, should load onto control planes nodes in the cluster
				if strings.HasPrefix(controllerOCIImage, ociImageDevPrefix) {
					logentry.Infof("Building dev binary for controller")
					parts := strings.Split(controllerOCIImage, ":")
					if len(parts) < 2 || parts[1] == "" {
						return fmt.Errorf("invalid dev image name: %s", controllerOCIImage)
					}
					buildPath := parts[1]
					// was a particular platform requested?
					platform := fmt.Sprintf("%s/%s", os.Getenv("GOOS"), os.Getenv("GOARCH"))
					if len(parts) >= 3 && parts[2] != "" {
						platform = parts[2]
					}

					tmpBinaryFile, err := os.CreateTemp("", "built-binary-*")
					if err != nil {
						return fmt.Errorf("creating tempfile: %w", err)
					}
					tmpBinaryFile.Close() // We just want the name
					tmpExecutable := tmpBinaryFile.Name()
					os.Chmod(tmpBinaryFile.Name(), 0755) // Make sure it's executable
					if err := buildGoBinary(buildPath, tmpExecutable, platform); err != nil {
						return fmt.Errorf("failed to build go binary: %v", err)
					}

					// load the binary into the cluster
					infile, err := os.Open(tmpExecutable)
					if err != nil {
						return fmt.Errorf("opening binary file: %w", err)
					}
					defer infile.Close()
					if err := c.LoadControllerToClusterNodes(ctx, infile); err != nil {
						return fmt.Errorf("failed to load controller binary into cluster: %v", err)
					}

					// the helm charts are already in the image, they will be modified appropriately to run
					// our runner image
				}
				logentry.Infof("Loading helm charts and pivoting to run on the cluster")
				if err := c.LoadHelmCharts(ctx); err != nil {
					return fmt.Errorf("failed to load helm charts onto the cluster: %v", err)
				}
				return nil
			}

			logentry.Infof("Not pivoting to run on the cluster, continuing to run locally")

			// we had noPivot, so keep running
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
				s := server.New(address, logentry, c, controlPlaneSecret, clusterProject, controlPlanePrefix, workerImageName, int(workerMemory), int(workerCPU))
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
	cmd.Flags().StringVar(&oxideAPIURL, flagOxideAPIURL, "", "Oxide API base URL; if not provided, will read from Kubernetes secret if available, or from ~/.config/oxide, or faill back to the default URL")
	cmd.Flags().StringVar(&oxideToken, flagOxideAPIToken, "", "Oxide API token; if starts with 'file:' then will read the key from the file; if none provided, will read from Kubernetes secret if available, or from ~/.config/oxide. Must not provide both --oxide-profile and --oxide-token")
	cmd.Flags().StringVar(&oxideProfile, flagOxideProfile, "", "Oxide profile to use; if none provided, will use default. Must not provide both --oxide-profile and --oxide-token")
	cmd.Flags().StringVar(&clusterProject, flagClusterProject, "ainekko-cluster", "Oxide project name for Kubernetes cluster")
	cmd.Flags().StringVar(&controlPlanePrefix, flagControlPlanePrefix, "ainekko-control-plane-", "Prefix for control plane instances")
	cmd.Flags().StringVar(&workerPrefix, flagWorkerPrefix, "ainekko-worker-", "Prefix for worker instances")
	cmd.Flags().UintVar(&controlPlaneCount, flagControlPlaneCount, 3, "Number of control plane instances to maintain")
	cmd.Flags().StringVar(&controlPlaneImageName, flagControlPlaneImageName, "debian-12-cloud", "Image to use for control plane instances")
	cmd.Flags().StringVar(&controlPlaneImageSource, flagControlPlaneImageSource, "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for control plane instances")
	cmd.Flags().IntVar(&controlPlaneImageBlocksize, flagControlPlaneImageBlocksize, defaultBlocksize, "Blocksize to use for control plane images")
	cmd.Flags().StringVar(&workerImageName, flagWorkerImageName, "debian-12-cloud", "Image to use for worker nodes")
	cmd.Flags().IntVar(&workerImageBlocksize, flagWorkerImageBlocksize, defaultBlocksize, "Blocksize to use for worker images")
	cmd.Flags().StringVar(&workerImageSource, flagWorkerImageSource, "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.raw", "Image to use for worker instances")
	cmd.Flags().UintVar(&workerRootDiskSizeGB, flagWorkerRootDiskSize, 0, "Size of root disk to attach to worker nodes, in GB; must be at least as large as the image size")
	cmd.Flags().UintVar(&controlPlaneRootDiskSizeGB, flagControlPlaneRootDiskSize, 0, "Size of root disk to attach to control plane nodes, in GB; must be at least as large as the image size")
	cmd.Flags().UintVar(&workerExtraDiskSizeGB, flagWorkerExtraDiskSize, 0, "Size of extra disk to attach to worker nodes, in GB. Leave as 0 for no extra disk.")
	cmd.Flags().UintVar(&controlPlaneExtraDiskSizeGB, flagControlPlaneExtraDiskSize, 0, "Size of extra disk to attach to control plane nodes, in GB. Leave as 0 for no extra disk.")
	cmd.Flags().Uint64Var(&controlPlaneMemory, flagControlPlaneMemory, 4, "Memory to allocate to each control plane node, in GB")
	cmd.Flags().UintVar(&workerCount, flagWorkerCount, 0, "Number of worker instances to create on startup and maintain, until changed via API")
	cmd.Flags().Uint64Var(&workerMemory, flagWorkerMemory, 16, "Memory to allocate to each worker node, in GB")
	cmd.Flags().Uint16Var(&controlPlaneCPU, flagControlPlaneCPU, 2, "vCPU count to allocate to each control plane node")
	cmd.Flags().Uint16Var(&workerCPU, flagWorkerCPU, 4, "vCPU count to allocate to each worker node")
	cmd.Flags().IntVar(&clusterInitWait, flagClusterInitWait, 5, "Time to wait for the first control plane node to be up and running (in minutes)")
	cmd.Flags().StringVar(&userSSHPublicKey, flagUserSSHPublicKey, "", "Path to public key to inject in all deployed cloud instances")
	cmd.Flags().StringVar(&kubeconfigPath, flagKubeconfig, "~/.kube/oxide-controller-config", "Path to save kubeconfig when generating new cluster, or to use for accessing existing cluster")
	cmd.Flags().BoolVar(&kubeconfigOverwrite, flagKubeconfigOverwrite, false, "Whether or not to override the kubeconfig file if it already exists and a new cluster is created")
	cmd.Flags().StringVar(&controlPlaneSecret, flagControlPlaneSecret, "oxide-controller-secret", "secret in Kubernetes cluster where the following are stored: join token, user ssh public key, controller ssh private/public keypair; will be in namespace provided by --namespace")
	cmd.Flags().StringVar(&controlPlaneNamespace, flagControlPlaneNamespace, "oxide-controller-system", "namespace in Kubernetes cluster where the resources live")
	cmd.Flags().BoolVar(&workerExternalIP, flagWorkerExternalIP, false, "Whether or not to assign an ephemeral public IP to the worker nodes, useful for debugging")
	cmd.Flags().BoolVar(&controlPlaneExternalIP, flagControlPlaneExternalIP, true, "Whether or not to assign an ephemeral public IP to the control plane nodes, needed to access cluster from outside sled, as well as for debugging")
	cmd.Flags().IntVarP(&verbose, flagVerbose, "v", 0, "set log level, 0 is info, 1 is debug, 2 is trace")
	cmd.Flags().StringVar(&address, flagAddress, ":8080", "Address to bind the server to")
	cmd.Flags().BoolVar(&noPivot, flagNoPivot, false, "Do not pivot this controller to run on the cluster itself after bringing the cluster up, instead continue long-running here")
	cmd.Flags().StringVar(&controllerOCIImage, flagControllerOCIImage, "aifoundryorg/oxide-controller:latest", "OCI image to use for the controller; if 'dev', will use the local build of the controller")
	cmd.Flags().IntVar(&controlLoopMins, flagControlLoopMins, 5, "How often to run the control loop, in minutes")
	cmd.Flags().IntVar(&imageParallelism, flagImageParallelism, 1, "How many parallel threads to use for uploading images to the sled")
	cmd.Flags().StringVar(&tailscaleAuthKey, flagTailscaleAuthKey, "", "Tailscale auth key to use for authentication, if none provided, will not join a tailnet; if starts with 'file:' then will read the key from the file")
	cmd.Flags().StringVar(&tailscaleAPIKey, flagTailscaleAPIKey, "", "Tailscale api key to use for listing tailnet nodes; if starts with 'file:' then will read the key from the file")
	cmd.Flags().StringVar(&tailscaleTag, flagTailscaleTag, "ainekko-k8s-node", "Tailscale tag to apply to nodes. Only used if --tailscale-auth-key is provided. Must exist as a valid tag in your tailnet.")
	cmd.Flags().StringVar(&tailscaleTailnet, flagTailscaleTailnet, "", "Tailscale tailnet to use to look for nodes joined to the tailnet. Must be provided if --tailscale-auth-key is provided.")

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
