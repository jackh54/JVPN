//go:build linux

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// configureNAT enables IPv4 forwarding and adds iptables MASQUERADE + FORWARD rules
// so clients on 10.8.0.0/16 can reach the internet. WAN interface is detected via "ip route get".
func configureNAT(tun string) error {
	wan, err := defaultWANInterface()
	if err != nil {
		return err
	}
	if err := ensureIPForward(); err != nil {
		return err
	}
	log.Printf("setup-nat: tun=%s wan=%s (MASQUERADE + FORWARD)", tun, wan)

	if err := iptablesTryAppend([]string{"-t", "nat", "-C", "POSTROUTING", "-s", "10.8.0.0/16", "-o", wan, "-j", "MASQUERADE"},
		[]string{"-t", "nat", "-A", "POSTROUTING", "-s", "10.8.0.0/16", "-o", wan, "-j", "MASQUERADE"}); err != nil {
		return fmt.Errorf("iptables nat: %w", err)
	}
	if err := iptablesTryAppend([]string{"-C", "FORWARD", "-i", tun, "-o", wan, "-j", "ACCEPT"},
		[]string{"-A", "FORWARD", "-i", tun, "-o", wan, "-j", "ACCEPT"}); err != nil {
		return fmt.Errorf("iptables forward tun->wan: %w", err)
	}
	if err := iptablesTryAppend([]string{"-C", "FORWARD", "-i", wan, "-o", tun, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		[]string{"-A", "FORWARD", "-i", wan, "-o", tun, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"}); err != nil {
		return fmt.Errorf("iptables forward wan->tun: %w", err)
	}
	return nil
}

// ensureIPForward turns on IPv4 forwarding. In Docker (even with host networking) /proc/sys is
// often read-only; if the host already has forwarding enabled we treat that as success.
func ensureIPForward() error {
	if ipForwardEnabled() {
		return nil
	}
	writeErr := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0644)
	if writeErr == nil && ipForwardEnabled() {
		return nil
	}
	cmd := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1")
	cmd.Stderr = os.Stderr
	sysErr := cmd.Run()
	if ipForwardEnabled() {
		return nil
	}
	return fmt.Errorf("enable ip_forward (set on host: sysctl -w net.ipv4.ip_forward=1): write=%v sysctl=%v", writeErr, sysErr)
}

func ipForwardEnabled() bool {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

func defaultWANInterface() (string, error) {
	out, err := exec.Command("ip", "route", "get", "8.8.8.8").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route get 8.8.8.8: %w: %s", err, strings.TrimSpace(string(out)))
	}
	s := string(out)
	const key = " dev "
	i := strings.Index(s, key)
	if i < 0 {
		return "", fmt.Errorf("parse WAN from route (no ' dev '): %q", strings.TrimSpace(s))
	}
	s = s[i+len(key):]
	j := strings.IndexByte(s, ' ')
	if j < 0 {
		return strings.TrimSpace(s), nil
	}
	return s[:j], nil
}

func iptablesTryAppend(check, appendRule []string) error {
	cmd := exec.Command("iptables", check...)
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("iptables", appendRule...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
