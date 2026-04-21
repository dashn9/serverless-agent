//go:build windows

package main

import (
	"os"
	"path/filepath"
)

func getAppsDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "apps"
	}
	return filepath.Join(cwd, "apps")
}

// chownToFluxRunner is a no-op on Windows — Unix ownership does not apply.
func chownToFluxRunner(path string) error {
	return nil
}
