package cluster

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aifoundry-org/oxide-controller/pkg/config"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// GetJoinToken retrieves a new k3s worker join token from the Kubernetes cluster
func (c *Cluster) GetJoinToken(ctx context.Context) (string, error) {
	conf, err := GetSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName)
	if err != nil {
		return "", err
	}
	return conf.K3sJoinToken, nil
}

// GetUserSSHPublicKey retrieves the SSH public key from the Kubernetes cluster
func (c *Cluster) GetUserSSHPublicKey(ctx context.Context) ([]byte, error) {
	conf, err := GetSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName)
	if err != nil {
		return nil, err
	}
	return []byte(conf.UserSSHPublicKey), nil
}

// GetOxideToken retrieves the oxide token from the Kubernetes cluster
func (c *Cluster) GetOxideToken(ctx context.Context) ([]byte, error) {
	conf, err := GetSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	return []byte(conf.OxideToken), nil
}

// GetOxideURL retrieves the oxide URL from the Kubernetes cluster
func (c *Cluster) GetOxideURL(ctx context.Context) ([]byte, error) {
	conf, err := GetSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	return []byte(conf.OxideURL), nil
}

// GetWorkerCount retrieves the targeted worker count from the Kubernetes cluster
func (c *Cluster) GetWorkerCount(ctx context.Context) (int, error) {
	conf, err := GetSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName)
	if err != nil {
		return 0, err
	}
	return int(conf.WorkerCount), nil
}

// SetWorkerCount sets the targeted worker count in the Kubernetes cluster
func (c *Cluster) SetWorkerCount(ctx context.Context, count int) error {
	conf, err := GetSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName)
	if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}
	conf.WorkerCount = uint(count)
	return setSecretConfig(ctx, c.apiConfig.Config, c.logger, c.config.ControlPlaneNamespace, c.config.SecretName, conf)
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

// setSecretValue sets a specific value in the secret
func setSecretValue(ctx context.Context, apiConfig *rest.Config, logger *log.Entry, namespace, secret, key string, value []byte) error {
	logger.Debugf("Setting secret value for key '%s' in secret '%s'", key, secret)
	secretData, err := getSecret(ctx, apiConfig, logger, namespace, secret)
	if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}
	secretData[key] = value
	clientset, err := getClientset(apiConfig)
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes clientset: %w", err)
	}
	return saveSecret(ctx, clientset, logger, namespace, secret, secretData)
}

// func getSecretValue(ctx context.Context, apiConfig *rest.Config, logger *log.Entry, namespace, secret, key string) ([]byte, error) {
func GetSecretConfig(ctx context.Context, apiConfig *rest.Config, logger *log.Entry, namespace, secret string) (*config.ControllerConfig, error) {
	logger.Debugf("Getting controller config from secret '%s'", secret)
	data, err := getSecretValue(ctx, apiConfig, logger, namespace, secret, secretKeyConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get controller config: %w", err)
	}
	var config config.ControllerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal controller config: %w", err)
	}
	return &config, nil
}

// func setSecretValue(ctx context.Context, apiConfig *rest.Config, logger *log.Entry, namespace, secret, key string) ([]byte, error) {
func setSecretConfig(ctx context.Context, apiConfig *rest.Config, logger *log.Entry, namespace, secret string, conf *config.ControllerConfig) error {
	logger.Debugf("Getting controller config from secret '%s'", secret)
	b, err := conf.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal controller config: %w", err)
	}
	return setSecretValue(ctx, apiConfig, logger, namespace, secret, secretKeyConfig, b)
}
