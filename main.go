package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

func unmountShare(mountPoint string) {
	log.Printf("Unmounting %s...", mountPoint)
	cmd := exec.Command("umount", mountPoint)
	if err := cmd.Run(); err != nil {
		log.Printf("Warning: Failed to unmount: %v", err)
	} else {
		log.Printf("Successfully unmounted %s", mountPoint)
	}
}

func main() {

	shareLocalName := flag.String("mount", "/mnt/agent", "Local mount point path")
	shareServer := flag.String("server", "//server/share", "SMB share path (//server/share)")
	username := flag.String("user", "admin", "SMB username")
	password := flag.String("pass", "admin", "SMB password")
	shareType := flag.String("type", "cifs", "Share type")
	pollInterval := flag.Int("interval", 5, "Poll interval in seconds")

	// Parse command line flags
	flag.Parse()

	//make mount point locally
	os.MkdirAll(*shareLocalName, 0755)

	// Build credentials string
	creds := fmt.Sprintf("username=%s,password=%s", *username, *password)

	// Mount the share
	cmd := exec.Command("mount", "-t", *shareType, *shareServer, *shareLocalName, "-o", creds)
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to mount share: %v", err)
	}

	log.Printf("Mounted %s to %s", *shareServer, *shareLocalName)

	deployer := NewDeployer(*shareLocalName)

	filewatcher := NewFileWatcher(*shareLocalName, time.Duration(*pollInterval)*time.Second)
	filewatcher.Subscribe(deployer.Handle)
	filewatcher.StartPolling()

}
