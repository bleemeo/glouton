package agent

import (
	"context"
	"errors"
	"fmt"
	"glouton/bleemeo"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/debouncer"
	"glouton/logger"
	"glouton/types"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	reloadDebouncerDelay  = 5 * time.Second
	reloadDebouncerPeriod = 10 * time.Second
)

var errWatcherNotStarted = errors.New("failed to start")

// ReloadState is used to keep some components alive during reloads.
type ReloadState interface {
	Bleemeo() bleemeoTypes.BleemeoReloadState
	DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error
	WatcherError() error
	Close()
}

type reloadState struct {
	bleemeo bleemeoTypes.BleemeoReloadState

	l            sync.Mutex
	watcherError error
}

func (rs *reloadState) Bleemeo() bleemeoTypes.BleemeoReloadState {
	return rs.bleemeo
}

func (rs *reloadState) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("reload-state.txt")
	if err != nil {
		return err
	}

	if rs.WatcherError() == nil {
		fmt.Fprintln(file, "The file watcher is running, Glouton will be reloaded on config changes.")
	} else {
		fmt.Fprintf(file, "An error occurred with the file watcher: %v\n", rs.watcherError)
	}

	return nil
}

func (rs *reloadState) WatcherError() error {
	rs.l.Lock()
	err := rs.watcherError
	rs.l.Unlock()

	return err
}

func (rs *reloadState) SetWatcherError(err error) {
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.watcherError = err
}

func (rs *reloadState) Close() {
	rs.bleemeo.Close()
}

type agentReloader struct {
	watcher             *fsnotify.Watcher
	configFilesFromFlag []string
	reloadState         *reloadState

	l              sync.Mutex
	agentIsRunning bool
}

// StartReloadManager starts the agent with a config file watcher, the agent is
// reloaded when a change is detected and the config is valid.
func StartReloadManager(configFilesFromFlag []string) {
	watcher, err := fsnotify.NewWatcher()

	a := agentReloader{
		watcher:             watcher,
		agentIsRunning:      false,
		configFilesFromFlag: configFilesFromFlag,
		reloadState: &reloadState{
			bleemeo: &bleemeo.ReloadState{},
		},
	}

	if err == nil {
		defer watcher.Close()
	} else {
		a.reloadState.SetWatcherError(err)
	}

	a.run()
}

func (a *agentReloader) run() {
	// Start watching config files.
	ctxWatcher, cancelWatcher := context.WithCancel(context.Background())
	defer cancelWatcher()

	reload := make(chan struct{}, 1)
	a.watchConfig(ctxWatcher, reload)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the agent for the first time.
	reload <- struct{}{}

	firstRun := true

	// Ticker needed to quit when the agent exited for another reason than a reload.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var wg sync.WaitGroup

	for {
		select {
		case <-reload:
			if !firstRun {
				logger.V(0).Printf("The config files have been modified, reloading agent...")

				cancel()
				wg.Wait()

				ctx, cancel = context.WithCancel(context.Background())
			}

			a.l.Lock()
			a.agentIsRunning = true
			a.l.Unlock()

			wg.Add(1)

			go func() {
				defer wg.Done()
				a.runAgent(ctx)
			}()

			firstRun = false

		case <-ticker.C:
			a.l.Lock()
			isRunning := a.agentIsRunning
			a.l.Unlock()

			if !isRunning {
				cancel()
				wg.Wait()

				a.reloadState.Close()

				return
			}
		}
	}
}

func (a *agentReloader) runAgent(ctx context.Context) {
	Run(ctx, a.reloadState, a.configFilesFromFlag)

	a.l.Lock()
	a.agentIsRunning = false
	a.l.Unlock()
}

// watchConfig sends an event to the reload channel when the config has changed
// and the agent needs to be reloaded.
func (a *agentReloader) watchConfig(ctx context.Context, reload chan struct{}) {
	if a.watcher == nil {
		a.reloadState.watcherError = errWatcherNotStarted

		return
	}

	myConfigFiles := a.configFilesFromFlag
	if len(myConfigFiles) == 0 || len(myConfigFiles[0]) == 0 {
		// Get default config files.
		cfg := config.Configuration{}
		cfg.Set("config_files", defaultConfig()["config_files"])
		myConfigFiles = cfg.StringList("config_files")
	}

	// Use a debouncer because fsnotify events are often duplicated.
	reloadAgentTarget := func(ctx context.Context) {
		if ctx.Err() == nil {
			// Validate config before reloading.
			if _, _, _, err := loadConfiguration(myConfigFiles, nil); err == nil {
				reload <- struct{}{}
			} else {
				logger.Printf("Error while loading configuration: %v", err)
			}
		}
	}

	reloadDebouncer := debouncer.New(ctx, reloadAgentTarget, reloadDebouncerDelay, reloadDebouncerPeriod)

	go a.receiveWatcherEvents(ctx, reloadDebouncer)

	for _, file := range myConfigFiles {
		if err := a.watcher.Add(file); err != nil {
			logger.V(0).Printf("Failed to add file to watcher: %v", err)
		}
	}
}

func (a *agentReloader) receiveWatcherEvents(ctx context.Context, reload *debouncer.Debouncer) {
	for ctx.Err() == nil {
		select {
		case event, ok := <-a.watcher.Events:
			if !ok {
				return
			}

			// If a file is moved to etc/glouton.conf, the watcher will not watch the
			// new file, so we need to add it again.
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				if exist, err := fileExist(event.Name); err != nil {
					logger.V(0).Printf("Failed to check if file %v exists: %v", event.Name, err)
				} else if exist {
					if err := a.watcher.Remove(event.Name); err != nil {
						logger.V(0).Printf("Failed to remove file in watcher: %v", err)
					}

					if err := a.watcher.Add(event.Name); err != nil {
						logger.V(0).Printf("Failed to add file to watcher: %v", err)
					}
				}
			}

			// Ignore temporary files generated by editors like .swp, ~.
			if !strings.HasSuffix(event.Name, ".conf") {
				continue
			}

			reload.Trigger()
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}

			a.reloadState.SetWatcherError(err)
			logger.V(0).Printf("File watcher error: %v", err)
		case <-ctx.Done():
			return
		}
	}
}

func fileExist(name string) (bool, error) {
	_, err := os.Stat(name)
	if err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}
