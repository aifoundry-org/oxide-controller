package cluster

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// getClientset returns a Kubernetes clientset, optionally using raw kubeconfig data.
// If kubeconfigRaw is nil or empty, it falls back to environment/default paths and in-cluster config.
func getClientset(kubeconfigRaw []byte) (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	if len(kubeconfigRaw) > 0 {
		// Load from raw kubeconfig bytes
		configAPI, err := clientcmd.Load(kubeconfigRaw)
		if err != nil {
			return nil, err
		}
		clientConfig := clientcmd.NewDefaultClientConfig(*configAPI, &clientcmd.ConfigOverrides{})
		config, err = clientConfig.ClientConfig()
		if err != nil {
			return nil, err
		}
	} else {
		// Try default loading rules (KUBECONFIG env var, ~/.kube/config, etc.)
		kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		)
		config, err = kubeconfig.ClientConfig()
		if err != nil {
			// Fall back to in-cluster config
			config, err = rest.InClusterConfig()
			if err != nil {
				return nil, fmt.Errorf("could not load kubeconfig from file/env or in-cluster")
			}
		}
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
