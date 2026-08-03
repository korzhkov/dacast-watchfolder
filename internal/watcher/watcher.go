package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/ysk/dacast-watchfolder/internal/applog"
)

const stableDuration = 3 * time.Second

type Handler func(path string)

type Watcher struct {
	Log     *applog.Logger
	OnFile  Handler
	mu      sync.Mutex
	pending map[string]*time.Timer
	folder  string
	fsw     *fsnotify.Watcher
}

func New(log *applog.Logger, onFile Handler) *Watcher {
	return &Watcher{
		Log:     log,
		OnFile:  onFile,
		pending: make(map[string]*time.Timer),
	}
}

func (w *Watcher) Scan(folder string) error {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if shouldIgnore(name) {
			continue
		}
		path := filepath.Join(folder, name)
		w.Log.Infof("startup scan found %s", name)
		w.schedule(path)
	}
	return nil
}

func (w *Watcher) Start(ctx context.Context, folder string) error {
	w.folder = folder
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.fsw = fsw
	if err := fsw.Add(folder); err != nil {
		_ = fsw.Close()
		return err
	}
	w.Log.Infof("watching folder %s", folder)

	go func() {
		defer fsw.Close()
		for {
			select {
			case <-ctx.Done():
				w.clearPending()
				return
			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				w.Log.Errorf("watcher error: %v", err)
			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}
				w.handleEvent(ev)
			}
		}
	}()
	return nil
}

func (w *Watcher) Stop() {
	w.clearPending()
	if w.fsw != nil {
		_ = w.fsw.Close()
		w.fsw = nil
	}
}

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
		return
	}
	info, err := os.Stat(ev.Name)
	if err != nil || info.IsDir() {
		return
	}
	// Top-level files only (no subfolders).
	if filepath.Clean(filepath.Dir(ev.Name)) != filepath.Clean(w.folder) {
		return
	}
	if shouldIgnore(filepath.Base(ev.Name)) {
		return
	}
	w.Log.Infof("detected change: %s (%s)", filepath.Base(ev.Name), ev.Op)
	w.schedule(ev.Name)
}

func (w *Watcher) schedule(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[path]; ok {
		t.Stop()
	}
	pathCopy := path
	w.pending[path] = time.AfterFunc(stableDuration, func() {
		w.mu.Lock()
		delete(w.pending, pathCopy)
		w.mu.Unlock()
		if !isSizeStable(pathCopy, stableDuration) {
			w.Log.Infof("file still growing, waiting: %s", filepath.Base(pathCopy))
			w.schedule(pathCopy)
			return
		}
		if w.OnFile != nil {
			w.OnFile(pathCopy)
		}
	})
}

func (w *Watcher) clearPending() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for p, t := range w.pending {
		t.Stop()
		delete(w.pending, p)
	}
}

func isSizeStable(path string, d time.Duration) bool {
	info1, err := os.Stat(path)
	if err != nil {
		return false
	}
	time.Sleep(d)
	info2, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info1.Size() == info2.Size() && info1.ModTime().Equal(info2.ModTime())
}

func shouldIgnore(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(name, "~$") {
		return true
	}
	if strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".part") || strings.HasSuffix(lower, ".crdownload") {
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}
