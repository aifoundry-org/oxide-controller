package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func buildGoBinary(buildPath, outputPath, platform string) error {
	cmd := exec.Command("go", "build", "-o", outputPath, buildPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if platform != "" {
		parts := strings.Split(platform, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid platform format: %s", platform)
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("GOOS=%s", parts[0]))
		cmd.Env = append(cmd.Env, fmt.Sprintf("GOARCH=%s", parts[1]))
	}

	return cmd.Run()
}
