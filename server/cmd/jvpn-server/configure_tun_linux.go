//go:build linux

package main

import (
	"os"
	"os/exec"
)

func configureTUN(name string) error {
	cmd := exec.Command("ip", "addr", "add", "10.8.0.1/24", "dev", name)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return err
	}
	cmd = exec.Command("ip", "link", "set", name, "up")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
