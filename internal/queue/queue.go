package queue

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/ysk/dacast-watchfolder/internal/applog"
	"github.com/ysk/dacast-watchfolder/internal/state"
	"github.com/ysk/dacast-watchfolder/internal/uploader"
)

type ProgressEvent struct {
	Path     string
	Uploaded int64
	Total    int64
	Part     int
	Parts    int
}

type Queue struct {
	State      *state.Store
	Log        *applog.Logger
	APIKey     func() string
	OnProgress func(ProgressEvent)

	mu      sync.Mutex
	jobs    chan string
	seen    map[string]struct{}
	cancel  context.CancelFunc
	running bool
	wg      sync.WaitGroup
}

func New(st *state.Store, log *applog.Logger, apiKey func() string) *Queue {
	return &Queue{
		State:  st,
		Log:    log,
		APIKey: apiKey,
		seen:   make(map[string]struct{}),
		jobs:   make(chan string, 256),
	}
}

func (q *Queue) Start(parent context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	q.cancel = cancel
	q.running = true
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.worker(ctx)
	}()
}

func (q *Queue) Stop() {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		return
	}
	q.Log.Infof("stopping upload queue (cancels in-flight upload)")
	q.cancel()
	q.running = false
	q.mu.Unlock()

	q.wg.Wait()
	q.drain()
}

func (q *Queue) drain() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		select {
		case <-q.jobs:
		default:
			q.seen = make(map[string]struct{})
			return
		}
	}
}

func (q *Queue) Enqueue(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	info, err := os.Stat(abs)
	if err != nil {
		q.Log.Errorf("enqueue skip %s: %v", abs, err)
		return
	}
	if info.IsDir() {
		return
	}

	q.mu.Lock()
	running := q.running
	q.mu.Unlock()
	if !running {
		// Persist intent; will be picked up on next Start via DB/scan.
		_ = q.State.UpsertQueued(abs, info.Size(), info.ModTime().UnixNano())
		return
	}

	done, err := q.State.IsDoneSameIdentity(abs, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		q.Log.Errorf("state check %s: %v", abs, err)
		return
	}
	if done {
		q.Log.Infof("already uploaded, skip %s", filepath.Base(abs))
		return
	}
	if err := q.State.UpsertQueued(abs, info.Size(), info.ModTime().UnixNano()); err != nil {
		q.Log.Errorf("upsert queued %s: %v", abs, err)
		return
	}

	q.mu.Lock()
	if _, ok := q.seen[abs]; ok {
		q.mu.Unlock()
		return
	}
	q.seen[abs] = struct{}{}
	q.mu.Unlock()

	select {
	case q.jobs <- abs:
		q.Log.Infof("queued %s", filepath.Base(abs))
	default:
		q.Log.Warnf("queue full, dropping %s", filepath.Base(abs))
		q.mu.Lock()
		delete(q.seen, abs)
		q.mu.Unlock()
	}
}

func (q *Queue) RequeuePendingFromDB() {
	list, err := q.State.ListNeedingUpload()
	if err != nil {
		q.Log.Errorf("list needing upload: %v", err)
		return
	}
	for _, u := range list {
		if _, err := os.Stat(u.Path); err != nil {
			continue
		}
		q.Enqueue(u.Path)
	}
}

func (q *Queue) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case path := <-q.jobs:
			if ctx.Err() != nil {
				q.mu.Lock()
				delete(q.seen, path)
				q.mu.Unlock()
				return
			}
			q.process(ctx, path)
			q.mu.Lock()
			delete(q.seen, path)
			q.mu.Unlock()
		}
	}
}

func (q *Queue) process(ctx context.Context, path string) {
	key := q.APIKey()
	if key == "" {
		q.Log.Errorf("API key not set; cannot upload %s", filepath.Base(path))
		_ = q.State.MarkFailed(path, "API key not set")
		return
	}
	up := &uploader.Uploader{
		State:  q.State,
		Log:    q.Log,
		APIKey: key,
		OnProgress: func(p string, uploaded, total int64, part, parts int) {
			if q.OnProgress != nil {
				q.OnProgress(ProgressEvent{Path: p, Uploaded: uploaded, Total: total, Part: part, Parts: parts})
			}
		},
	}
	q.Log.Infof("starting upload %s", filepath.Base(path))
	if err := up.Upload(ctx, path); err != nil {
		if ctx.Err() != nil {
			q.Log.Warnf("upload cancelled %s (will resume on next start)", filepath.Base(path))
			return
		}
		q.Log.Errorf("upload failed %s: %v", filepath.Base(path), err)
	}
}
