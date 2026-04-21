//go:build linux

package main

import (
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

func getAppsDir() string {
	u, err := user.Lookup("flux-runner")
	if err != nil {
		log.Printf("Failed to lookup flux-runner: %v, using ./apps", err)
		return "./apps"
	}
	return filepath.Join(u.HomeDir, "apps")
}

func chownToFluxRunner(path string) error {
	u, err := user.Lookup("flux-runner")
	if err != nil {
		return err
	}
	uid, _ := strconv.ParseInt(u.Uid, 10, 32)
	gid, _ := strconv.ParseInt(u.Gid, 10, 32)

	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(name, int(uid), int(gid))
	})
}
