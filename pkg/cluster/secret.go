package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// getSecretValue retrieves a specific value from the secret
func getSecretValue(ctx context.Context, apiConfig *rest.Config, logger *log.Entry, namespace, secret, key string) ([]byte, error) {
	logger.Debugf("Getting secret value for key '%s' from secret '%s'", key, secret)
	secretData, err := getSecret(ctx, apiConfig, logger, namespace, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	value, ok := secretData[key]
	if !ok {
		return nil, NewSecretKeyNotFoundError(key)
	}
	// no need to base64-decode, since the API returns the raw secret
	return value, nil
}

// GetJoinToken retrieves a new k3s worker join token from the Kubernetes cluster
func (c *Cluster) GetJoinToken(ctx context.Context) (string, error) {
	value, err := getSecretValue(ctx, c.apiConfig.Config, c.logger, c.namespace, c.secretName, secretKeyJoinToken)
	if err != nil {
		return "", err
	}
	// convert to string
	valStr := string(value)
	// remove trailing newlines
	return strings.TrimSuffix(valStr, "\n"), nil
}

// GetUserSSHPublicKey retrieves the SSH public key from the Kubernetes cluster
func (c *Cluster) GetUserSSHPublicKey(ctx context.Context) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, c.apiConfig.Config, c.logger, c.namespace, c.secretName, secretKeyUserSSH)
	if err != nil {
		return nil, err
	}
	return pubkey, nil
}

// GetOxideToken retrieves the oxide token from the Kubernetes cluster
func (c *Cluster) GetOxideToken(ctx context.Context) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, c.apiConfig.Config, c.logger, c.namespace, c.secretName, secretKeyOxideToken)
	if err != nil {
		return nil, err
	}
	return pubkey, nil
}

// GetOxideURL retrieves the oxide URL from the Kubernetes cluster
func (c *Cluster) GetOxideURL(ctx context.Context) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, c.apiConfig.Config, c.logger, c.namespace, c.secretName, secretKeyOxideURL)
	if err != nil {
		return nil, err
	}
	return pubkey, nil
}

// GetWorkerCount retrieves the targeted worker count from the Kubernetes cluster
func (c *Cluster) GetWorkerCount(ctx context.Context) (int, error) {
	workerCount, err := getSecretValue(ctx, c.apiConfig.Config, c.logger, c.namespace, c.secretName, secretKeyWorkerCount)
	if err != nil {
		return 0, err
	}
	// convert to string
	valStr := string(workerCount)
	// remove trailing newlines
	valStr = strings.TrimSuffix(valStr, "\n")
	// convert to int
	count, err := strconv.Atoi(valStr)
	if err != nil {
		return 0, fmt.Errorf("failed to convert worker count to int: %w", err)
	}
	return count, nil
}

// SetWorkerCount sets the targeted worker count in the Kubernetes cluster
func (c *Cluster) SetWorkerCount(ctx context.Context, count int) error {
	secretMap, err := getSecret(ctx, c.apiConfig.Config, c.logger, c.namespace, c.secretName)
	if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}
	secretMap[secretKeyWorkerCount] = []byte(fmt.Sprintf("%d", count))
	if err := saveSecret(ctx, c.clientset, c.logger, c.namespace, c.secretName, secretMap); err != nil {
		return fmt.Errorf("failed to save secret: %w", err)
	}
	return nil
}

// GetOxideToken retrieves the oxide token from the Kubernetes cluster
func GetOxideToken(ctx context.Context, restConfig *rest.Config, logger *log.Entry, namespace, secretName string) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, restConfig, logger, namespace, secretName, secretKeyOxideToken)
	if err != nil {
		return nil, err
	}
	return pubkey, nil
}

// GetOxideURL retrieves the oxide URL from the Kubernetes cluster
func GetOxideURL(ctx context.Context, restConfig *rest.Config, logger *log.Entry, namespace, secretName string) ([]byte, error) {
	pubkey, err := getSecretValue(ctx, restConfig, logger, namespace, secretName, secretKeyOxideURL)
	if err != nil {
		return nil, err
	}
	return pubkey, nil
}

// getSecret gets the secret with all of our important information
func getSecret(ctx context.Context, config *rest.Config, logger *log.Entry, namespace, name string) (map[string][]byte, error) {
	logger.Debugf("Getting secret %s/%s", namespace, name)

	clientset, err := getClientset(config)
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
func saveSecret(ctx context.Context, clientset *kubernetes.Clientset, logger *log.Entry, namespace, name string, data map[string][]byte) error {
	logger.Debugf("Saving secret %s with keymap size %d", name, len(data))

	secretsClient := clientset.CoreV1().Secrets(namespace)

	// Prepare the secret object
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
		Type: v1.SecretTypeOpaque,
	}

	// Check if the secret exists
	_, err := secretsClient.Get(context.TODO(), name, metav1.GetOptions{})
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
