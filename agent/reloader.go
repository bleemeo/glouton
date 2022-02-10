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
	Close()
}

type reloadState struct {
	bleemeo      bleemeoTypes.BleemeoReloadState
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

	if rs.watcherError == nil {
		fmt.Fprintln(file, "The file watcher is running, Glouton will be reloaded on config changes.")
	} else {
		fmt.Fprintf(file, "An error occurred with the file watcher: %v\n", rs.watcherError)
		fmt.Fprintln(file, "Glouton will not be reloaded on config changes.")
	}

	return nil
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

func StartReloadManager(watcher *fsnotify.Watcher, configFilesFromFlag []string) {
	a := agentReloader{
		watcher:             watcher,
		agentIsRunning:      false,
		configFilesFromFlag: configFilesFromFlag,
		reloadState: &reloadState{
			bleemeo: &bleemeo.ReloadState{},
		},
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
			_, _, _, err := loadConfiguration(myConfigFiles, nil)
			if err == nil {
				reload <- struct{}{}
			}
		}
	}
	reloadDebouncer := debouncer.New(ctx, reloadAgentTarget, reloadDebouncerDelay, reloadDebouncerPeriod)

	go func() {
		for ctx.Err() == nil {
			select {
			case event, ok := <-a.watcher.Events:
				if !ok {
					return
				}

				// Only watch for write changes.
				if event.Op&fsnotify.Write != fsnotify.Write {
					continue
				}

				// Ignore temporary files generated by editors like .swp, ~.
				if !strings.HasSuffix(event.Name, ".conf") {
					continue
				}

				reloadDebouncer.Trigger()
			case err, ok := <-a.watcher.Errors:
				if !ok {
					return
				}

				a.reloadState.watcherError = err
				logger.V(0).Printf("File watcher error: %v", err)
			case <-ctx.Done():
				return
			}
		}
	}()

	for _, file := range myConfigFiles {
		_ = a.watcher.Add(file)
	}
}
