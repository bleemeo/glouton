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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	reloadDebouncerDelay  = 5 * time.Second
	reloadDebouncerPeriod = 30 * time.Second
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

	l             sync.Mutex
	watcherError  error
	reloadCounter int
	lastReload    time.Time
}

func (rs *reloadState) Bleemeo() bleemeoTypes.BleemeoReloadState {
	return rs.bleemeo
}

func (rs *reloadState) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("reload-state.txt")
	if err != nil {
		return err
	}

	if err := rs.WatcherError(); err == nil {
		fmt.Fprintln(file, "The file watcher is running, Glouton will be reloaded on config changes.")
	} else {
		fmt.Fprintf(file, "An error occurred with the file watcher: %v\n", err)
	}

	if count := rs.reloadCount(); count == 0 {
		fmt.Fprintln(file, "The agent has never been reloaded.")
	} else {
		fmt.Fprintf(file, "The agent has been reloaded %v times.\n", count)
	}

	if lastReload := rs.lastReloadDate(); !lastReload.IsZero() {
		fmt.Fprintf(file, "The last reload was done on %v.\n", lastReload)
	}

	return nil
}

func (rs *reloadState) WatcherError() error {
	rs.l.Lock()
	err := rs.watcherError
	rs.l.Unlock()

	return err
}

func (rs *reloadState) setWatcherError(err error) {
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.watcherError = err
}

func (rs *reloadState) reloadCount() int {
	rs.l.Lock()
	count := rs.reloadCounter
	rs.l.Unlock()

	return count
}

func (rs *reloadState) incrementReloadCount() {
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.reloadCounter++
}

func (rs *reloadState) lastReloadDate() time.Time {
	rs.l.Lock()
	lastReload := rs.lastReload
	rs.l.Unlock()

	return lastReload
}

func (rs *reloadState) setLastReloadDate(lastReload time.Time) {
	rs.l.Lock()
	defer rs.l.Unlock()

	rs.lastReload = lastReload
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
			bleemeo: bleemeo.NewReloadState(),
		},
	}

	if err == nil {
		defer watcher.Close()
	} else {
		a.reloadState.setWatcherError(err)
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
				a.reloadState.incrementReloadCount()
				a.reloadState.setLastReloadDate(time.Now())

				cancel()
				wg.Wait()

				ctx, cancel = context.WithCancel(context.Background())
			}

			a.l.Lock()
			a.agentIsRunning = true
			a.l.Unlock()

			wg.Add(1)

			first := firstRun

			go func() {
				defer wg.Done()
				a.runAgent(ctx, first)
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

func (a *agentReloader) runAgent(ctx context.Context, firstRun bool) {
	Run(ctx, a.reloadState, a.configFilesFromFlag, firstRun)

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

	// Get config files.
	myConfigFiles := a.configFilesFromFlag
	if len(myConfigFiles) == 0 || len(myConfigFiles[0]) == 0 {
		const configFileKey = "config_files"

		cfg := config.Configuration{}
		cfg.Set(configFileKey, defaultConfig()[configFileKey])

		_, err := cfg.LoadEnv(configFileKey, config.TypeStringList, keyToEnvironmentName(configFileKey))
		if err != nil {
			logger.Printf("Failed to load environment variable: %v", err)
		}

		myConfigFiles = cfg.StringList(configFileKey)
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

	for _, dir := range myConfigFiles {
		fileInfo, err := os.Stat(dir)
		if err != nil {
			logger.V(2).Printf("Failed to stat file %v: %v", dir, err)

			continue
		}

		// Only watch parent dirs, not files to avoid losing notifications when files are renamed.
		if !fileInfo.IsDir() {
			dir = filepath.Dir(dir)
		}

		if err := a.watcher.Add(dir); err != nil {
			logger.V(2).Printf("Failed to add file to watcher: %v", err)
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

			// Ignore temporary files generated by editors like .swp, ~.
			if !strings.HasSuffix(event.Name, ".conf") {
				continue
			}

			reload.Trigger()
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}

			a.reloadState.setWatcherError(err)
			logger.V(0).Printf("File watcher error: %v", err)
		case <-ctx.Done():
			return
		}
	}
}
