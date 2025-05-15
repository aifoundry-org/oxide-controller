package cluster

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// createNamespace ensures a namespace exists or creates it.
func createNamespace(ctx context.Context, clientset *kubernetes.Clientset, namespace string) error {
	nsClient := clientset.CoreV1().Namespaces()

	// Check if the namespace already exists
	_, err := nsClient.Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		// Already exists
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to check namespace existence: %w", err)
	}

	// Create the namespace
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err = nsClient.Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace %q: %w", namespace, err)
	}

	return nil
}
