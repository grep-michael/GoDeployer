package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type FileWatcher struct {
	Path        string
	Interval    time.Duration
	subscribers []FileChangeHandler
}
type FileChangeEvent struct {
	RelPath    string
	FullPath   string
	ChangeType string // "created", "modified", "deleted"
	Size       int64
	ModTime    time.Time
}
type FileState struct {
	ModTime time.Time
	Size    int64
}
type FileChangeHandler func(event FileChangeEvent)

func NewFileWatcher(path string, interval time.Duration) *FileWatcher {
	return &FileWatcher{
		Path:        path,
		Interval:    interval,
		subscribers: make([]FileChangeHandler, 0),
	}
}
func (fw *FileWatcher) SetPath(path string) {
	fw.Path = path
}
func (fw *FileWatcher) Subscribe(handler FileChangeHandler) {
	fw.subscribers = append(fw.subscribers, handler)
	log.Printf("Subscribed handler (total: %d)", len(fw.subscribers))
}

func (fw *FileWatcher) StartPolling() {

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	done := make(chan bool)

	go func() {
		fw.pollloop()
		done <- true
	}()
	select {
	case <-sigChan:
		log.Println("\nReceived interrupt signal, shutting down...")
		unmountShare(fw.Path)
		os.Exit(0)
	case <-done:
		log.Println("Watch ended")
		unmountShare(fw.Path)
	}
}

func (fw *FileWatcher) pollloop() {
	knownFiles := make(map[string]FileState)

	log.Printf("Starting to poll %s every %v", fw.Path, fw.Interval)

	ticker := time.NewTicker(fw.Interval)
	defer ticker.Stop()

	// Do initial scan
	fw.scanFiles(fw.Path, knownFiles, true)

	for range ticker.C {
		fw.scanFiles(fw.Path, knownFiles, false)
	}
}

func (fw *FileWatcher) scanFiles(sharePath string, knownFiles map[string]FileState, isInitial bool) {
	currentFiles := make(map[string]FileState)

	err := filepath.Walk(sharePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error accessing path %s: %v", path, err)
			return nil
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path from root
		relPath, err := filepath.Rel(sharePath, path)
		if err != nil {
			log.Printf("Error getting relative path for %s: %v", path, err)
			return nil
		}

		// Store current file state
		state := FileState{
			ModTime: info.ModTime(),
			Size:    info.Size(),
		}
		currentFiles[relPath] = state

		// Check if file is new or modified
		if oldState, exists := knownFiles[relPath]; exists {
			// Check if modified
			if !oldState.ModTime.Equal(state.ModTime) || oldState.Size != state.Size {
				log.Printf("MODIFIED: %s (size: %d bytes, modified: %s)",
					relPath, state.Size, state.ModTime.Format(time.RFC3339))

				fw.Notify(FileChangeEvent{
					ChangeType: "modified",
					RelPath:    relPath,
					FullPath:   path,
					Size:       info.Size(),
					ModTime:    info.ModTime(),
				})
			}
		} else {
			// New file
			if !isInitial {
				log.Printf("NEW FILE: %s (size: %d bytes)", relPath, state.Size)
				fw.Notify(FileChangeEvent{
					ChangeType: "created",
					RelPath:    relPath,
					FullPath:   path,
					Size:       info.Size(),
					ModTime:    info.ModTime(),
				})
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error walking directory: %v", err)
	}

	// Check for deleted files
	if !isInitial {
		for relPath := range knownFiles {
			if _, exists := currentFiles[relPath]; !exists {
				log.Printf("DELETED: %s", relPath)
				fw.Notify(FileChangeEvent{
					ChangeType: "deleted",
					RelPath:    relPath,
				})
			}
		}
	}

	// Update known files
	for k, v := range currentFiles {
		knownFiles[k] = v
	}

	// Remove deleted files from map
	for k := range knownFiles {
		if _, exists := currentFiles[k]; !exists {
			delete(knownFiles, k)
		}
	}
}

func (fw *FileWatcher) Notify(event FileChangeEvent) {
	for i, handler := range fw.subscribers {
		log.Printf("Notifying subscriber %d for %s (%s)", i+1, event.RelPath, event.ChangeType)
		handler(event)
	}
}
