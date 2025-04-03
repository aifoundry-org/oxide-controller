package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// getSecretValue retrieves a specific value from the secret
func getSecretValue(ctx context.Context, kubeconfig []byte, secret, key string) ([]byte, error) {
	secretData, err := getSecret(ctx, kubeconfig, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	value, ok := secretData[key]
	if !ok {
		return nil, fmt.Errorf("key '%s' not found in secret", key)
	}
	decodedValue, err := base64.StdEncoding.DecodeString(string(value))
	if err != nil {
		return nil, fmt.Errorf("failed to decode value: %w", err)
	}
	return decodedValue, nil
}

// GetJoinToken retrieves a new k3s worker join token from the Kubernetes cluster
func GetJoinToken(ctx context.Context, kubeconfig []byte, secret string) (string, error) {
	value, err := getSecretValue(ctx, kubeconfig, secret, secretKeyJoinToken)
	if err != nil {
		return "", fmt.Errorf("failed to get join token: %w", err)
	}
	// convert to string
	return string(value), nil
}

// GetUserSSHPublicKey retrieves the SSH public key from the Kubernetes cluster
func GetUserSSHPublicKey(ctx context.Context, kubeconfig []byte, secret string) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, kubeconfig, secret, secretKeyUserSSH)
	if err != nil {
		return nil, fmt.Errorf("failed to get user SSH public key: %w", err)
	}
	return pubkey, nil
}

// getSecret gets the secret with all of our important information
func getSecret(ctx context.Context, kubeconfigRaw []byte, secret string) (map[string][]byte, error) {
	parts := strings.SplitN(secret, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid secret format %s, expected <namespace>/<name>", secret)
	}
	namespace, name := parts[0], parts[1]

	clientset, err := getClientset(kubeconfigRaw)
	if err != nil {
		return nil, err
	}

	// Get the secret
	secretObj, err := clientset.CoreV1().Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return secretObj.Data, nil
}

// saveSecret save a secret to the Kubernetes cluster
func saveSecret(secretRef string, kubeconfig []byte, data map[string][]byte) error {
	// Parse namespace and name from <namespace>/<name>
	parts := strings.SplitN(secretRef, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid secret reference: expected <namespace>/<name>")
	}
	namespace, name := parts[0], parts[1]

	clientset, err := getClientset(kubeconfig)
	if err != nil {
		return err
	}

	// Prepare the secret object
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
		Type: v1.SecretTypeOpaque,
	}

	secretsClient := clientset.CoreV1().Secrets(namespace)

	// Check if the secret exists
	_, err = secretsClient.Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			_, err = secretsClient.Create(context.TODO(), secret, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Update existing secret
	_, err = secretsClient.Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}
