package oxide

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oxidecomputer/oxide.go/oxide"
	"github.com/pelletier/go-toml"
)

// everything in this file is a duplicate of the code in oxide.go, but
// all of that is private, and we need to be able to get the actual token and host
// to store in the cluster secret, so we had to duplicate it here.
// When that becomes public, or if they create service accounts on oxide,
// we can remove this code.

const (
	defaultConfigDir string = ".config/oxide"
	configFile       string = "config.toml"
	credentialsFile  string = "credentials.toml"
)

func GetProfile(cfg *oxide.Config) (string, string, error) {
	configDir := cfg.ConfigDir
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("unable to find user's home directory: %w", err)
		}
		configDir = filepath.Join(homeDir, defaultConfigDir)
	}

	profile := cfg.Profile

	// Use explicitly configured profile over default when both are set.
	if cfg.UseDefaultProfile && profile == "" {
		configPath := filepath.Join(configDir, configFile)

		var err error
		profile, err = defaultProfile(configPath)
		if err != nil {
			return "", "", fmt.Errorf("failed to get default profile from %q: %w", configPath, err)
		}
	}

	credentialsPath := filepath.Join(configDir, credentialsFile)
	host, token, err := parseCredentialsFile(credentialsPath, profile)
	if err != nil {
		return "", "", fmt.Errorf("failed to get credentials for profile %q from %q: %w", profile, credentialsPath, err)
	}

	return host, token, nil
}

// defaultProfile returns the default profile from config.toml, if present.
func defaultProfile(configPath string) (string, error) {
	configFile, err := toml.LoadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to open config: %w", err)
	}

	if profileName := configFile.Get("default-profile"); profileName != nil {
		return profileName.(string), nil
	}

	return "", errors.New("no default profile set")
}

// parseCredentialsFile parses a credentials.toml and returns the token and host
// associated with the requested profile.
func parseCredentialsFile(credentialsPath, profileName string) (string, string, error) {
	if profileName == "" {
		return "", "", errors.New("no profile name provided")
	}

	credentialsFile, err := toml.LoadFile(credentialsPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open %q: %v", credentialsPath, err)
	}

	profile, ok := credentialsFile.Get("profile." + profileName).(*toml.Tree)
	if !ok {
		return "", "", errors.New("profile not found")
	}

	var hostTokenErr error
	token, ok := profile.Get("token").(string)
	if !ok {
		hostTokenErr = errors.New("token not found")
	}

	host, ok := profile.Get("host").(string)
	if !ok {
		hostTokenErr = errors.Join(errors.New("host not found"))
	}

	return host, token, hostTokenErr
}
