//go:build !darwin && !linux

package main

import "fmt"

func configureTUN(name string) error {
	return fmt.Errorf("configureTUN: use Linux or macOS, or assign 10.8.0.1/16 to %q manually", name)
}
