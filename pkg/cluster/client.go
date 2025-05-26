package cluster

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type ConfigSource int

const (
	ConfigSourceProvidedKubeconfig ConfigSource = iota
	ConfigSourceDefaultKubeconfig
	ConfigSourceInCluster
)

type Config struct {
	*rest.Config
	Source ConfigSource
}

// getClientset returns a Kubernetes clientset, optionally using raw kubeconfig data.
// If kubeconfigRaw is nil or empty, it falls back to environment/default paths and in-cluster config.
func getClientset(config *rest.Config) (*kubernetes.Clientset, error) {
	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

func GetRestConfig(kubeconfigRaw []byte) (*Config, error) {
	if len(kubeconfigRaw) > 0 {
		configAPI, err := clientcmd.Load(kubeconfigRaw)
		if err != nil {
			return nil, err
		}
		clientConfig := clientcmd.NewDefaultClientConfig(*configAPI, &clientcmd.ConfigOverrides{})
		conf, err := clientConfig.ClientConfig()
		return &Config{Config: conf, Source: ConfigSourceProvidedKubeconfig}, err
	}
	// try in-cluster
	conf, err := rest.InClusterConfig()
	if err == nil {
		return &Config{Config: conf, Source: ConfigSourceInCluster}, err
	}

	// fall back to default config
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	config, err := kubeconfig.ClientConfig()
	// if we did not find one, return an error, but indicate that we could not find any,
	// rather than an error in the config itself
	if err != nil && clientcmd.IsEmptyConfig(err) {
		return nil, nil
	}
	return &Config{Config: config, Source: ConfigSourceDefaultKubeconfig}, err
}
