package cluster

import "testing"

func TestGenerateCloudConfig(t *testing.T) {
	tests := []struct {
		name           string
		nodeType       string
		initCluster    bool
		controlPlaneIP string
		joinToken      string
		pubkey         []string
		extraDisk      string
		expected       string
		err            error
	}{
		{"server-init-pubkey", "server", true, "10.0.0.5", "joinme", []string{"somekey"}, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - server --cluster-init --tls-san 10.0.0.5 --node-external-ip 10.0.0.5 --tls-san ${PRIVATE_IP} --tls-san ${PUBLIC_IP}
users:
  - name: root
    shell: /bin/bash
    ssh-authorized-keys:
      - somekey
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"server-init-nopubkey", "server", true, "10.0.0.5", "joinme", nil, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - server --cluster-init --tls-san 10.0.0.5 --node-external-ip 10.0.0.5 --tls-san ${PRIVATE_IP} --tls-san ${PUBLIC_IP}
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"server-join-pubkey", "server", false, "10.0.0.5", "joinme", []string{"somekey"}, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - server --server https://10.0.0.5:6443 --token joinme --tls-san ${PRIVATE_IP} --tls-san ${PUBLIC_IP}
users:
  - name: root
    shell: /bin/bash
    ssh-authorized-keys:
      - somekey
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"server-join-nopubkey", "server", false, "10.0.0.5", "joinme", nil, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - server --server https://10.0.0.5:6443 --token joinme --tls-san ${PRIVATE_IP} --tls-san ${PUBLIC_IP}
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"agent-pubkey", "agent", false, "10.0.0.5", "joinme", []string{"somekey"}, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - agent --server https://10.0.0.5:6443 --token joinme
users:
  - name: root
    shell: /bin/bash
    ssh-authorized-keys:
      - somekey
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"agent-nopubkey", "agent", false, "10.0.0.5", "joinme", nil, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    curl -sfL https://get.k3s.io | sh -s - agent --server https://10.0.0.5:6443 --token joinme
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateCloudConfig(tt.nodeType, tt.initCluster, tt.controlPlaneIP, tt.joinToken, tt.pubkey, tt.extraDisk)
			if err != nil && err != tt.err {
				t.Errorf("expected error %v, got %v", tt.err, err)
			}
			resultStr := string(result)
			if resultStr != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, resultStr)
			}
		})
	}
}
