package cluster

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

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

func getRestConfig(kubeconfigRaw []byte) (*rest.Config, error) {
	if len(kubeconfigRaw) > 0 {
		configAPI, err := clientcmd.Load(kubeconfigRaw)
		if err != nil {
			return nil, err
		}
		clientConfig := clientcmd.NewDefaultClientConfig(*configAPI, &clientcmd.ConfigOverrides{})
		return clientConfig.ClientConfig()
	}

	// Try local default config first
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	config, err := kubeconfig.ClientConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to in-cluster
	return rest.InClusterConfig()
}
