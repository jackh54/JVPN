//go:build !linux

package main

import "fmt"

func configureNAT(string) error {
	return fmt.Errorf("-setup-nat is only supported on Linux")
}
