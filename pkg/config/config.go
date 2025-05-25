package config

import "encoding/json"

type ControllerConfig struct {
	WorkerSpec            NodeSpec `json:"worker-spec,omitempty"`
	WorkerCount           uint     `json:"worker-count,omitempty"`
	ControlPlaneCount     uint     `json:"control-plane-count,omitempty"`
	ControlPlaneSpec      NodeSpec `json:"control-plane-spec,omitempty"`
	ControlPlaneIP        string   `json:"control-plane-ip,omitempty"`
	UserSSHPublicKey      string   `json:"user-ssh-public-key,omitempty"`
	K3sJoinToken          string   `json:"k3s-join-token,omitempty"`
	SystemSSHPublicKey    string   `json:"system-ssh-public-key,omitempty"`
	SystemSSHPrivateKey   string   `json:"system-ssh-private-key,omitempty"`
	OxideToken            string   `json:"oxide-token,omitempty"`
	OxideURL              string   `json:"oxide-url,omitempty"`
	ClusterProject        string   `json:"cluster-project,omitempty"`
	ControlPlaneNamespace string   `json:"control-plane-namespace,omitempty"`
	SecretName            string   `json:"secret-name,omitempty"`
	Address               string   `json:"address,omitempty"`
	ControlLoopMins       int      `json:"control-loop-mins,omitempty"`
	ImageParallelism      int      `json:"image-parallelism,omitempty"`
	TailscaleAuthKey      string   `json:"tailscale-auth-key,omitempty"`
	TailscaleAPIKey       string   `json:"tailscale-api-key,omitempty"`
	TailscaleTag          string   `json:"tailscale-tag,omitempty"`
	TailscaleTailnet      string   `json:"tailscale-tailnet,omitempty"`
}

// Node represents a Kubernetes node
type NodeSpec struct {
	Prefix           string `json:"prefix"`
	Image            Image  `json:"image"`
	MemoryGB         int    `json:"memoryGB"`
	CPUCount         int    `json:"cpuCount"`
	RootDiskSize     int    `json:"diskSize"`
	ExtraDiskSize    int    `json:"extraDiskSize"`
	ExternalIP       bool   `json:"externalIP"`
	TailscaleAuthKey string `json:"tailscaleAuthKey"`
	TailscaleTag     string `json:"tailscaleTag"`
}

type Image struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Blocksize int    `json:"blocksize"`
	ID        string `json:"id"`
	Size      int    `json:"size"`
}

func (c *ControllerConfig) ToJSON() ([]byte, error) {
	return json.Marshal(c)
}
