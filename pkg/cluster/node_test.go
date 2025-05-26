package cluster

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

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
    [ -n "${PRIVATE_IP}" ] && K3S_PRIVATE_IP_ARG="--tls-san ${PRIVATE_IP}"
    echo "Private IP: ${PRIVATE_IP}"
    echo "Private IP k3s arg: ${K3S_PRIVATE_IP_ARG}"
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    [ -n "${PUBLIC_IP}" ] && K3S_PUBLIC_IP_ARG="--tls-san ${PUBLIC_IP}"
    echo "Public IP: ${PUBLIC_IP}"
    echo "Public IP k3s arg: ${K3S_PUBLIC_IP_ARG}"
    curl -sfL https://get.k3s.io | sh -s - server --cluster-init --tls-san 10.0.0.5 --node-external-ip 10.0.0.5 ${K3S_PRIVATE_IP_ARG} ${K3S_PUBLIC_IP_ARG}
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
    [ -n "${PRIVATE_IP}" ] && K3S_PRIVATE_IP_ARG="--tls-san ${PRIVATE_IP}"
    echo "Private IP: ${PRIVATE_IP}"
    echo "Private IP k3s arg: ${K3S_PRIVATE_IP_ARG}"
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    [ -n "${PUBLIC_IP}" ] && K3S_PUBLIC_IP_ARG="--tls-san ${PUBLIC_IP}"
    echo "Public IP: ${PUBLIC_IP}"
    echo "Public IP k3s arg: ${K3S_PUBLIC_IP_ARG}"
    curl -sfL https://get.k3s.io | sh -s - server --cluster-init --tls-san 10.0.0.5 --node-external-ip 10.0.0.5 ${K3S_PRIVATE_IP_ARG} ${K3S_PUBLIC_IP_ARG}
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"server-join-pubkey", "server", false, "10.0.0.5", "joinme", []string{"somekey"}, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    [ -n "${PRIVATE_IP}" ] && K3S_PRIVATE_IP_ARG="--tls-san ${PRIVATE_IP}"
    echo "Private IP: ${PRIVATE_IP}"
    echo "Private IP k3s arg: ${K3S_PRIVATE_IP_ARG}"
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    [ -n "${PUBLIC_IP}" ] && K3S_PUBLIC_IP_ARG="--tls-san ${PUBLIC_IP}"
    echo "Public IP: ${PUBLIC_IP}"
    echo "Public IP k3s arg: ${K3S_PUBLIC_IP_ARG}"
    curl -sfL https://get.k3s.io | sh -s - server --server https://10.0.0.5:6443 --token joinme ${K3S_PRIVATE_IP_ARG} ${K3S_PUBLIC_IP_ARG}
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
    [ -n "${PRIVATE_IP}" ] && K3S_PRIVATE_IP_ARG="--tls-san ${PRIVATE_IP}"
    echo "Private IP: ${PRIVATE_IP}"
    echo "Private IP k3s arg: ${K3S_PRIVATE_IP_ARG}"
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    [ -n "${PUBLIC_IP}" ] && K3S_PUBLIC_IP_ARG="--tls-san ${PUBLIC_IP}"
    echo "Public IP: ${PUBLIC_IP}"
    echo "Public IP k3s arg: ${K3S_PUBLIC_IP_ARG}"
    curl -sfL https://get.k3s.io | sh -s - server --server https://10.0.0.5:6443 --token joinme ${K3S_PRIVATE_IP_ARG} ${K3S_PUBLIC_IP_ARG}
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
		{"agent-pubkey", "agent", false, "10.0.0.5", "joinme", []string{"somekey"}, "", `#cloud-config
runcmd:
  - |
    PRIVATE_IP=$(hostname -I | awk '{print $1}')
    [ -n "${PRIVATE_IP}" ] && K3S_PRIVATE_IP_ARG="--tls-san ${PRIVATE_IP}"
    echo "Private IP: ${PRIVATE_IP}"
    echo "Private IP k3s arg: ${K3S_PRIVATE_IP_ARG}"
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    [ -n "${PUBLIC_IP}" ] && K3S_PUBLIC_IP_ARG="--tls-san ${PUBLIC_IP}"
    echo "Public IP: ${PUBLIC_IP}"
    echo "Public IP k3s arg: ${K3S_PUBLIC_IP_ARG}"
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
    [ -n "${PRIVATE_IP}" ] && K3S_PRIVATE_IP_ARG="--tls-san ${PRIVATE_IP}"
    echo "Private IP: ${PRIVATE_IP}"
    echo "Private IP k3s arg: ${K3S_PRIVATE_IP_ARG}"
    PUBLIC_IP=$(curl -s https://ifconfig.me)
    [ -n "${PUBLIC_IP}" ] && K3S_PUBLIC_IP_ARG="--tls-san ${PUBLIC_IP}"
    echo "Public IP: ${PUBLIC_IP}"
    echo "Public IP k3s arg: ${K3S_PUBLIC_IP_ARG}"
    curl -sfL https://get.k3s.io | sh -s - agent --server https://10.0.0.5:6443 --token joinme
ssh_pwauth: false
disable_root: false
allow_public_ssh_keys: true
`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateCloudConfig(tt.nodeType, tt.initCluster, tt.controlPlaneIP, tt.joinToken, tt.pubkey, tt.extraDisk, "", "")
			if err != nil && err != tt.err {
				t.Errorf("expected error %v, got %v", tt.err, err)
			}
			resultStr := string(result)
			if diff := cmp.Diff(tt.expected, resultStr); diff != "" {
				t.Errorf("Mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
