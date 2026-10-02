//go:build darwin

package store

import "golang.org/x/sys/unix"

func embeddingMachineCPUIdentity() string {
	value, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil {
		return "cpu-identity-unavailable"
	}
	return value
}
