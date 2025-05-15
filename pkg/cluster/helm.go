package cluster

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	ctrlr "github.com/aifoundry-org/oxide-controller"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func (c *Cluster) loadHelmCharts(ctx context.Context) error {
	rel, err := installOrUpgradeEmbeddedChart(ctrlr.ChartFiles, c.namespace, c.apiConfig, map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to install/upgrade helm chart: %w", err)
	}
	c.logger.Infof("Release %s deployed to namespace %s", rel.Name, rel.Namespace)
	return nil
}

// loadActionConfigWithRestConfig returns an initialized Helm action.Configuration using a pre-built *rest.Config
func loadActionConfigWithRestConfig(namespace string, restConfig *rest.Config) (*action.Configuration, error) {
	cfg := new(action.Configuration)
	restClientGetter := &StaticRESTClientGetter{
		RestConfig: restConfig,
		Namespace:  namespace,
	}
	driver := "secrets"
	err := cfg.Init(restClientGetter, namespace, driver, func(format string, v ...interface{}) {
		fmt.Printf(format+"\n", v...)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init helm config: %w", err)
	}

	return cfg, nil
}

func installOrUpgradeEmbeddedChart(chartFS embed.FS, namespace string, restConfig *rest.Config, values map[string]interface{}) (*release.Release, error) {
	actionConfig, err := loadActionConfigWithRestConfig(namespace, restConfig)
	if err != nil {
		return nil, err
	}

	chart, err := loadChartFromFS(chartFS)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedded chart: %w", err)
	}

	client := action.NewUpgrade(actionConfig)
	client.Namespace = namespace
	client.Install = true
	client.Atomic = true

	release, err := client.Run("oxide-controller", chart, values)
	if err != nil {
		return nil, fmt.Errorf("failed to install/upgrade chart: %w", err)
	}

	return release, nil
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
	return nil
}

func (s *StaticRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return nil, fmt.Errorf("discovery client not implemented")
}

func (s *StaticRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return nil, fmt.Errorf("RESTMapper not implemented")
}

func (s *StaticRESTClientGetter) ToNamespace() (string, error) {
	return s.Namespace, nil
}
