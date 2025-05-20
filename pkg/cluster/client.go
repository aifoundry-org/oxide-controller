package cluster

import (
	"fmt"

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
		fmt.Println("Using provided kubeconfig")
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
	fmt.Printf("Using in-cluster err: %v\n", err)
	fmt.Printf("Using in-cluster config: %+v\n", conf)
	if err == nil {
		return &Config{Config: conf, Source: ConfigSourceInCluster}, err
	}

	// fall back to default config
	fmt.Println("Trying default kubeconfig")
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	config, err := kubeconfig.ClientConfig()
	fmt.Printf("Using default kubeconfig err: %v\n", err)
	fmt.Printf("Using default kubeconfig config: %+v\n", config)
	return &Config{Config: config, Source: ConfigSourceDefaultKubeconfig}, err
}
