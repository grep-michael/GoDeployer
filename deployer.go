package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

/*
Copys SourceLocation/* to DeployLocation/
then executes executable using DeployLocation as its cwd
*/

const (
	CONFIG_ID = "deploy.json"
)

type DeployConfig struct {
	DeployLocation       string   `json:"deploy_location"`
	Executable           string   `json:"executable"`
	Args                 []string `json:"args"`
	SourceLocation       string   `json:"source_location"` //location in share to copy source code from
	EnvironmentVariables []string `json:"env_variables"`   //a list of strings in the format KEY=VALUE
}

func LoadDeployConfig(share string, panicOnFailure bool) *DeployConfig {
	//we assume the config will be in the share folder
	config_location := filepath.Join(share, CONFIG_ID)

	data, err := os.ReadFile(config_location)
	if err != nil {
		err_msg := fmt.Sprintf("failed to load config: %v\n", err)
		if panicOnFailure {
			panic(err_msg)
		}
		log.Print(err_msg)
		return nil
	}

	var config DeployConfig
	if err := json.Unmarshal(data, &config); err != nil {
		err_msg := fmt.Sprintf("failed to marshel config, json format probably wrong: %v\n", err)
		if panicOnFailure {
			panic(err_msg)
		}
		log.Print(err_msg)
		return nil
	}

	return &config
}

type Deployer struct {
	MountLocation string
	Config        *DeployConfig
	currentCmd    *exec.Cmd
	cmdMutex      sync.Mutex
	isRunning     bool
}

func NewDeployer(mountLocation string) *Deployer {
	cfg := LoadDeployConfig(mountLocation, true)
	deployer := &Deployer{
		MountLocation: mountLocation,
		Config:        cfg,
	}
	return deployer
}

func (d *Deployer) Handle(event FileChangeEvent) {
	fmt.Println(event)
	if event.RelPath == CONFIG_ID {
		d.reloadConfig()
		d.Redeploy()
		return
	}
	if d.isSourceFile(event.RelPath) {
		log.Printf("Source file changed: %s, redeploying...", event.RelPath)
		d.Redeploy()
	}

}
func (d *Deployer) isSourceFile(relPath string) bool {
	sourceDir := d.Config.SourceLocation
	inSrcLoc := strings.HasPrefix(
		filepath.Clean(relPath),
		filepath.Clean(sourceDir),
	)
	return inSrcLoc
}

func (d *Deployer) reloadConfig() {
	cfg := LoadDeployConfig(d.MountLocation, false)
	if cfg != nil {
		d.Config = cfg
	}
}

func (d *Deployer) Kill() error {
	d.cmdMutex.Lock()
	defer d.cmdMutex.Unlock()

	if d.currentCmd == nil || d.currentCmd.Process == nil {
		log.Println("No process to kill")
		return nil
	}

	log.Printf("Killing process PID: %d", d.currentCmd.Process.Pid)

	// Send SIGTERM for graceful shutdown
	if err := d.currentCmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("Failed to send SIGTERM: %v", err)
		// Force kill if SIGTERM fails
		if err := d.currentCmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	// Wait for process to exit (with timeout)
	done := make(chan error, 1)
	go func() {
		done <- d.currentCmd.Wait()
	}()

	select {
	case <-done:
		log.Println("Process terminated successfully")
	case <-time.After(5 * time.Second):
		log.Println("Process didn't exit gracefully, force killing...")
		d.currentCmd.Process.Kill()
	}

	d.currentCmd = nil
	d.isRunning = false
	return nil
}

func (d *Deployer) Deploy() error {
	d.cmdMutex.Lock()
	defer d.cmdMutex.Unlock()

	// Copy source files
	sourcePath := filepath.Join(d.MountLocation, d.Config.SourceLocation)
	deployPath := d.Config.DeployLocation

	log.Printf("Copying from %s to %s", sourcePath, deployPath)
	if err := copyDir(sourcePath, deployPath); err != nil {
		return fmt.Errorf("failed to copy source: %w", err)
	}
	log.Printf("Starting executable: %s %v", d.Config.Executable, d.Config.Args)

	//set up env
	env := os.Environ()
	env = append(env, d.Config.EnvironmentVariables...)

	// Add display variables for X11
	env = append(env, "DISPLAY=:0") // Primary display
	env = append(env, fmt.Sprintf("XAUTHORITY=/home/%s/.Xauthority", os.Getenv("USER")))

	// Create command
	cmd := exec.Command(d.Config.Executable, d.Config.Args...)
	cmd.Dir = deployPath
	cmd.Env = env

	// Pipe output to logs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start executable: %w", err)
	}

	d.currentCmd = cmd
	d.isRunning = true

	log.Printf("Process started with PID: %d", cmd.Process.Pid)

	// Monitor process in goroutine
	go func() {
		err := cmd.Wait()
		d.cmdMutex.Lock()
		d.isRunning = false
		d.cmdMutex.Unlock()

		if err != nil {
			log.Printf("Process exited with error: %v", err)
		} else {
			log.Println("Process exited normally")
		}
	}()

	return nil
}

func (d *Deployer) Redeploy() error {
	log.Println("Starting redeployment...")

	// Kill existing process
	if err := d.Kill(); err != nil {
		log.Printf("Error killing existing process: %v", err)
	}

	// Wait a moment for cleanup
	time.Sleep(500 * time.Millisecond)

	// Deploy new version
	return d.Deploy()
}
func (d *Deployer) IsRunning() bool {
	d.cmdMutex.Lock()
	defer d.cmdMutex.Unlock()
	return d.isRunning
}

//	--------------
//	Help functions
//	--------------

func copyDir(src, dst string) error {
	// Create destination directory
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Copy file
		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, data, 0644)
}
