package util

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// SSHKeyPair generates a new SSH key pair
func SSHKeyPair() ([]byte, []byte, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	// Convert to OpenSSH public key format
	pubKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}

	// Encode private key to PEM as openssh
	privPEM := new(bytes.Buffer)
	sshPrivKey, err := ssh.MarshalPrivateKey(priv, "ainekko-oxide-controller")
	if err != nil {
		return nil, nil, err
	}
	if err := pem.Encode(privPEM, sshPrivKey); err != nil {
		return nil, nil, err
	}
	return privPEM.Bytes(), ssh.MarshalAuthorizedKey(pubKey), nil
}

// RunSSHCommand run a command on a remote server via SSH
func RunSSHCommand(user, addr string, privPEM []byte, command string) ([]byte, error) {
	// Parse the private key
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Create SSH client config
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // for testing only; validate host keys in production
	}

	// Connect to the SSH server
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SSH: %w", err)
	}
	defer client.Close()

	// Start a session
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Capture stdout
	output, err := session.Output(command)
	if err != nil {
		return nil, fmt.Errorf("command failed: %w", err)
	}

	return output, nil
}
