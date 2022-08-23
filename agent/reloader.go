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
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	reloadDebouncerDelay  = 5 * time.Second
	reloadDebouncerPeriod = 30 * time.Second
)

var errWatcherDisabled = errors.New("reload disabled")

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

	switch err = rs.WatcherError(); {
	case err == nil:
		fmt.Fprintln(file, "The file watcher is running, Glouton will be reloaded on config changes.")
	case errors.Is(err, errWatcherDisabled):
		fmt.Fprintln(file, "Reloading is disabled.")
	default:
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
func StartReloadManager(configFilesFromFlag []string, reloadDisabled bool) {
	var (
		watcher *fsnotify.Watcher
		err     error
	)

	if !reloadDisabled {
		watcher, err = fsnotify.NewWatcher()
	} else {
		err = errWatcherDisabled
	}

	a := agentReloader{
		watcher:             watcher,
		agentIsRunning:      false,
		configFilesFromFlag: configFilesFromFlag,
		reloadState: &reloadState{
			bleemeo: bleemeo.NewReloadState(),
		},
	}

	if err == nil && !reloadDisabled {
		defer watcher.Close()
	} else if err != nil {
		a.reloadState.setWatcherError(err)
	}

	a.run()
}

func (a *agentReloader) run() {
	// The sighup handler needs to be set up very early to catch the SIGHUP signal
	// received from apt post-update hook.
	sighupChan := make(chan os.Signal, 1)
	signal.Notify(sighupChan, syscall.SIGHUP)

	// Start watching config files.
	ctxWatcher, cancelWatcher := context.WithCancel(context.Background())
	defer cancelWatcher()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stop Glouton when one of these signals is received.
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	// Start Windows service.
	initOSSpecificParts(stopChan)

	reload := make(chan struct{}, 1)
	a.watchConfig(ctxWatcher, reload)

	// Run the agent for the first time.
	reload <- struct{}{}

	firstRun := true

	// Ticker needed to quit when the agent exited for another reason than a reload.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var wg sync.WaitGroup

out:
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
				defer types.ProcessPanic()
				defer wg.Done()

				a.runAgent(ctx, sighupChan, first)
			}()

			firstRun = false

		case <-ticker.C:
			a.l.Lock()
			isRunning := a.agentIsRunning
			a.l.Unlock()

			if !isRunning {
				break out
			}
		case sig := <-stopChan:
			logger.Printf("Received signal %s, stopping...", sig)

			break out
		}
	}

	// Stop Glouton.
	cancel()
	wg.Wait()

	signal.Stop(sighupChan)
	close(sighupChan)
	signal.Stop(stopChan)
	close(stopChan)
	a.reloadState.Close()
}

func (a *agentReloader) runAgent(ctx context.Context, signalChan chan os.Signal, firstRun bool) {
	Run(ctx, a.reloadState, a.configFilesFromFlag, signalChan, firstRun)

	a.l.Lock()
	a.agentIsRunning = false
	a.l.Unlock()
}

// watchConfig sends an event to the reload channel when the config has changed
// and the agent needs to be reloaded.
func (a *agentReloader) watchConfig(ctx context.Context, reload chan struct{}) {
	if a.watcher == nil {
		return
	}

	// Get config files.
	configFiles := a.configFilesFromFlag
	if len(configFiles) == 0 || len(configFiles[0]) == 0 {
		const configFileKey = "config_files"

		cfg := config.Configuration{}
		cfg.Set(configFileKey, defaultConfig()[configFileKey])

		_, err := cfg.LoadEnv(configFileKey, config.TypeStringList, keyToEnvironmentName(configFileKey))
		if err != nil {
			logger.Printf("Failed to load environment variable: %v", err)
		}

		configFiles = cfg.StringList(configFileKey)
	}

	// Use a debouncer because fsnotify events are often duplicated.
	reloadAgentTarget := func(ctx context.Context) {
		if ctx.Err() == nil {
			// Validate config before reloading.
			if _, _, _, err := loadConfiguration(configFiles, nil); err == nil {
				reload <- struct{}{}
			} else {
				logger.Printf("Error while loading configuration: %v", err)
			}
		}
	}

	reloadDebouncer := debouncer.New(ctx, reloadAgentTarget, reloadDebouncerDelay, reloadDebouncerPeriod)

	go func() {
		defer types.ProcessPanic()

		a.receiveWatcherEvents(ctx, reloadDebouncer)
	}()

	for _, dir := range configFiles {
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

			if !isValidConfigFile(event.Name) {
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

// Match temporary files generated by vim, nano and emacs.
var temporaryFilesRegex = regexp.MustCompile(`.*(\.(sw[a-p]|save)|~)`)

// isValidConfigFile returns whether a configuration file as a valid name.
// Temporary files generated by editors like .swp, ~, are invalid.
func isValidConfigFile(name string) bool {
	isValid := !temporaryFilesRegex.MatchString(name)

	return isValid
}
