package cluster

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	ctrlr "github.com/aifoundry-org/oxide-controller"

	"github.com/distribution/distribution/v3/reference"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

const (
	chartName     = "oxide-controller"
	drivers       = "secrets"
	chartWaitTime = 300 * time.Minute
)

// LoadHelmCharts loads the embedded Helm charts into the cluster.
func (c *Cluster) LoadHelmCharts(ctx context.Context) error {
	var (
		repo, tag     string
		err           error
		useHostBinary bool
	)
	// if flagged as dev mode, use the host binary, else determine it from the image
	if c.ociImage == devModeOCIImage {
		// Use the host binary for dev mode
		useHostBinary = true
		repo = "busybox"
		tag = "latest"
	} else {
		repo, tag, err = parseImage(c.ociImage)
		if err != nil {
			return fmt.Errorf("failed to parse OCI image: %w", err)
		}

	}

	if repo == "dev" {
	}
	values := map[string]interface{}{
		"createNamespace": false,
		"namespace":       c.namespace,
		"secretName":      c.secretName,
		"useHostBinary":   useHostBinary,
		"image": map[string]interface{}{
			"repository": repo,
			"tag":        tag,
			"port":       8080,
		},
	}
	chartFiles, err := ctrlr.Chart()
	if err != nil {
		return fmt.Errorf("failed to load chart files: %w", err)
	}
	fmt.Printf("values: %+v\n", values)
	rel, err := installOrUpgradeEmbeddedChart(chartFiles, c.namespace, c.apiConfig, values)
	fmt.Printf("manifest: %s\n", rel.Manifest)
	if err != nil {
		return fmt.Errorf("failed to install/upgrade helm chart: %w", err)
	}
	c.logger.Infof("Release %s deployed to namespace %s", rel.Name, rel.Namespace)
	return nil
}

func parseImage(image string) (repo string, tag string, err error) {
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", "", fmt.Errorf("invalid image reference: %w", err)
	}

	// Always remove the default `latest` tag from canonical name if not explicitly set
	named := reference.TagNameOnly(ref)

	repo = named.Name()

	// Try to extract tag if available
	if tagged, ok := ref.(reference.Tagged); ok {
		tag = tagged.Tag()
	} else {
		tag = "latest"
	}

	return repo, tag, nil
}

// loadActionConfigWithRestConfig returns an initialized Helm action.Configuration using a pre-built *rest.Config
func loadActionConfigWithRestConfig(namespace string, restConfig *rest.Config) (*action.Configuration, error) {
	cfg := new(action.Configuration)
	restClientGetter := &StaticRESTClientGetter{
		RestConfig: restConfig,
		Namespace:  namespace,
	}
	err := cfg.Init(restClientGetter, namespace, drivers, func(format string, v ...interface{}) {
		fmt.Printf(format+"\n", v...)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init helm config: %w", err)
	}

	return cfg, nil
}

func installOrUpgradeEmbeddedChart(chartFS fs.FS, namespace string, restConfig *rest.Config, values map[string]interface{}) (*release.Release, error) {
	// we clone the config because we will change some settings, and do not want it to affect the original
	rc := rest.CopyConfig(restConfig)
	rc.QPS = 100
	rc.Burst = 200
	actionConfig, err := loadActionConfigWithRestConfig(namespace, rc)
	if err != nil {
		return nil, err
	}

	chart, err := loadChartFromFS(chartFS)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedded chart: %w", err)
	}

	histClient := action.NewHistory(actionConfig)
	histClient.Max = 1
	_, err = histClient.Run(chartName)

	if err == driver.ErrReleaseNotFound {
		// Install new release
		install := action.NewInstall(actionConfig)
		install.ReleaseName = chartName
		install.Namespace = namespace
		install.Atomic = true
		install.Timeout = chartWaitTime

		return install.Run(chart, values)
	} else if err != nil {
		return nil, fmt.Errorf("failed to check release history: %w", err)
	}

	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = namespace
	upgrade.Atomic = true
	upgrade.Timeout = chartWaitTime

	return upgrade.Run(chartName, chart, values)
}

func loadChartFromFS(chartFS fs.FS) (*chart.Chart, error) {
	var files []*loader.BufferedFile

	err := fs.WalkDir(chartFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(chartFS, path)
		if err != nil {
			return err
		}
		// path must be relative to the chart root
		relPath := strings.TrimPrefix(path, "./")
		files = append(files, &loader.BufferedFile{
			Name: relPath,
			Data: data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return loader.LoadFiles(files)
}

type StaticRESTClientGetter struct {
	RestConfig *rest.Config
	Namespace  string
}

func (s *StaticRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return s.RestConfig, nil
}

func (s *StaticRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.NewNonInteractiveClientConfig(
		*api.NewConfig(),
		"",
		&clientcmd.ConfigOverrides{
			ClusterDefaults: clientcmd.ClusterDefaults,
			CurrentContext:  "",
			Context: api.Context{
				Namespace: s.Namespace,
			},
		},
		nil,
	)
}

func (s *StaticRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	disc, err := discovery.NewDiscoveryClientForConfig(s.RestConfig)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(disc), nil
}

func (s *StaticRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := s.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient), nil
}

func (s *StaticRESTClientGetter) ToNamespace() (string, error) {
	return s.Namespace, nil
}
