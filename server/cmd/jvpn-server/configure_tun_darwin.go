//go:build darwin

package main

import (
	"os"
	"os/exec"
)

func configureTUN(name string) error {
	// macOS utun uses a point-to-point ioctl; local and "destination" addresses are both required.
	// 10.8.0.1 = server side on the TUN; 10.8.0.2 matches the first client slot used by the pool.
	cmd := exec.Command("ifconfig", name, "inet", "10.8.0.1", "10.8.0.2", "netmask", "255.255.0.0", "mtu", "1420", "up")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
