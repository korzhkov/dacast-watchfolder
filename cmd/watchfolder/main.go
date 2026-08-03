package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"

	"github.com/lxn/walk"

	"github.com/ysk/dacast-watchfolder/internal/appdir"
	"github.com/ysk/dacast-watchfolder/internal/applog"
	"github.com/ysk/dacast-watchfolder/internal/config"
	"github.com/ysk/dacast-watchfolder/internal/queue"
	"github.com/ysk/dacast-watchfolder/internal/state"
	"github.com/ysk/dacast-watchfolder/internal/ui"
	"github.com/ysk/dacast-watchfolder/internal/watcher"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("fatal: %v\n\n%s", r, debug.Stack())
			fmt.Fprintln(os.Stderr, msg)
			if dir, err := appdir.Dir(); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "crash.log"), []byte(msg), 0o644)
			}
			walk.MsgBox(nil, "Dacast Watchfolder", fmt.Sprintf("Application crashed:\n%v\n\nDetails saved to %%AppData%%\\DacastWatchfolder\\crash.log", r), walk.MsgBoxIconError)
		}
	}()

	log, err := applog.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "log: %v\n", err)
		os.Exit(1)
	}
	defer log.Close()

	cfgStore, err := config.Load()
	if err != nil {
		log.Errorf("config load: %v", err)
		os.Exit(1)
	}

	st, err := state.Open()
	if err != nil {
		log.Errorf("state open: %v", err)
		os.Exit(1)
	}
	defer st.Close()

	log.Infof("Dacast Watchfolder started")

	var (
		mu      sync.Mutex
		running bool
		cancel  context.CancelFunc
		watch   *watcher.Watcher
		rootCtx = context.Background()
	)

	q := queue.New(st, log, func() string {
		return cfgStore.Get().APIKey
	})

	ctrl := &ui.Controller{
		Cfg: cfgStore,
		Log: log,
		IsRunning: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return running
		},
		OnSave: func(cfg config.Config) error {
			if cfg.WatchFolder != "" {
				info, err := os.Stat(cfg.WatchFolder)
				if err != nil {
					return fmt.Errorf("watch folder: %w", err)
				}
				if !info.IsDir() {
					return fmt.Errorf("watch folder is not a directory")
				}
			}
			if err := cfgStore.Set(cfg); err != nil {
				return err
			}
			log.Infof("settings saved")
			return nil
		},
	}

	ctrl.OnStop = func() {
		mu.Lock()
		defer mu.Unlock()
		if !running {
			return
		}
		log.Infof("Stop watching: cancelling watcher and any in-flight upload")
		ctrl.SetStatus("Stopping…")
		if cancel != nil {
			cancel()
		}
		if watch != nil {
			watch.Stop()
			watch = nil
		}
		q.Stop()
		running = false
		ctrl.SetStatus("Idle")
		log.Infof("stopped")
	}

	ctrl.OnStart = func() error {
		cfg := cfgStore.Get()
		if cfg.APIKey == "" {
			return fmt.Errorf("API key is required")
		}
		if cfg.WatchFolder == "" {
			return fmt.Errorf("watch folder is required")
		}
		info, err := os.Stat(cfg.WatchFolder)
		if err != nil {
			return fmt.Errorf("watch folder: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("watch folder is not a directory")
		}

		mu.Lock()
		defer mu.Unlock()
		if running {
			return nil
		}

		ctx, c := context.WithCancel(rootCtx)
		cancel = c

		q.Start(ctx)

		watch = watcher.New(log, func(path string) {
			q.Enqueue(path)
		})
		if err := watch.Scan(cfg.WatchFolder); err != nil {
			c()
			q.Stop()
			return err
		}
		q.RequeuePendingFromDB()
		if err := watch.Start(ctx, cfg.WatchFolder); err != nil {
			c()
			q.Stop()
			watch.Stop()
			return err
		}
		running = true
		log.Infof("watching started: %s", cfg.WatchFolder)
		ctrl.SetStatus("Watching")
		return nil
	}

	q.OnProgress = ctrl.UpdateProgress
	ctrl.Run()
}
