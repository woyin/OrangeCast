//go:build !darwin

package store

import (
	"os"
	"strings"
)

func embeddingMachineCPUIdentity() string {
	contents, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "cpu-identity-unavailable"
	}
	for _, line := range strings.Split(string(contents), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "model name" || key == "Hardware" {
			return strings.TrimSpace(parts[1])
		}
	}
	return "cpu-identity-unavailable"
}
