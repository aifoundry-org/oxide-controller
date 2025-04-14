package cluster

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type CloudConfig struct {
	Hostname           string           `yaml:"hostname,omitempty"`
	ManageEtcHosts     bool             `yaml:"manage_etc_hosts,omitempty"`
	Packages           []string         `yaml:"packages,omitempty"`
	WriteFiles         []WriteFile      `yaml:"write_files,omitempty"`
	RunCmd             MultiLineStrings `yaml:"runcmd,omitempty"`
	Users              []User           `yaml:"users,omitempty"`
	SSHPWAuth          bool             `yaml:"ssh_pwauth"`            // disables password logins
	DisableRoot        bool             `yaml:"disable_root"`          // ensure root isn't disabled
	AllowPublicSSHKeys bool             `yaml:"allow_public_ssh_keys"` // allow public SSH keys
}

type WriteFile struct {
	Path        string `yaml:"path"`
	Permissions string `yaml:"permissions,omitempty"`
	Content     string `yaml:"content"`
}

type User struct {
	Name              string   `yaml:"name"`
	Shell             string   `yaml:"shell,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh-authorized-keys,omitempty"`
}

type MultiLineStrings [][]string

// MarshalYAML formats each inner slice as a block scalar string
func (m MultiLineStrings) MarshalYAML() (interface{}, error) {
	seq := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
	}
	for _, lines := range m {
		joined := strings.Join(lines, "\n")
		scalar := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: joined + "\n",
			Style: yaml.LiteralStyle, // this is the '|'
		}
		seq.Content = append(seq.Content, scalar)
	}
	return seq, nil
}
