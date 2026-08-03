package uploader

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ysk/dacast-watchfolder/internal/applog"
	"github.com/ysk/dacast-watchfolder/internal/dacast"
	"github.com/ysk/dacast-watchfolder/internal/state"
)

const (
	DefaultPartSize = 8 << 20 // 8 MiB (> 5 MiB required)
	MaxPartsPerSig  = 100
	MaxPartRetries  = 10
	MaxAPIRetries   = 10
)

type ProgressFunc func(path string, uploaded, total int64, part, parts int)

type Uploader struct {
	State      *state.Store
	Log        *applog.Logger
	APIKey     string
	PartSize   int64
	OnProgress ProgressFunc
}

func (u *Uploader) partSize() int64 {
	if u.PartSize > 0 {
		return u.PartSize
	}
	return DefaultPartSize
}

func (u *Uploader) Upload(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	size := info.Size()
	mtimeNs := info.ModTime().UnixNano()
	partSize := u.partSize()
	if size == 0 {
		return fmt.Errorf("empty file")
	}

	rec, err := u.State.Get(path)
	if err != nil {
		_ = u.State.UpsertQueued(path, size, mtimeNs)
		rec, err = u.State.Get(path)
		if err != nil {
			return err
		}
	}

	client := dacast.New(u.APIKey)
	resume := rec.Status == state.StatusUploading &&
		rec.UploaderID != "" &&
		rec.S3Path != "" &&
		rec.PartSize > 0 &&
		rec.Size == size &&
		rec.MtimeNs == mtimeNs

	var uploaderID, s3Path string
	if resume {
		uploaderID = rec.UploaderID
		s3Path = rec.S3Path
		partSize = rec.PartSize
		u.Log.Infof("resuming upload %s (part_size=%d)", filepath.Base(path), partSize)
	} else {
		if rec.Status == state.StatusUploading {
			u.Log.Warnf("file identity changed or session incomplete for %s; starting new multipart", filepath.Base(path))
			_ = u.State.ResetSession(path)
		}
		u.Log.Infof("init multipart for %s (%d bytes)", filepath.Base(path), size)
		initResp, err := withRetry(ctx, u.Log, "init-multipart", func() (*dacast.InitResponse, error) {
			return client.InitMultipart(ctx, filepath.Base(path))
		})
		if err != nil {
			_ = u.State.MarkFailed(path, err.Error())
			return err
		}
		uploaderID = initResp.UploaderID
		s3Path = initResp.S3Path
		if err := u.State.ClearParts(path); err != nil {
			return err
		}
		if err := u.State.MarkUploading(path, uploaderID, s3Path, partSize); err != nil {
			return err
		}
	}

	err = u.uploadParts(ctx, client, path, size, uploaderID, s3Path, partSize)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return err
	}
	// Session likely expired / invalid — one full restart keeping file identity.
	if isSessionFatal(err) {
		u.Log.Warnf("multipart session invalid for %s: %v; restarting from init", filepath.Base(path), err)
		_ = u.State.ResetSession(path)
		initResp, initErr := withRetry(ctx, u.Log, "init-multipart", func() (*dacast.InitResponse, error) {
			return client.InitMultipart(ctx, filepath.Base(path))
		})
		if initErr != nil {
			_ = u.State.MarkFailed(path, initErr.Error())
			return initErr
		}
		if err := u.State.MarkUploading(path, initResp.UploaderID, initResp.S3Path, u.partSize()); err != nil {
			return err
		}
		if err := u.uploadParts(ctx, client, path, size, initResp.UploaderID, initResp.S3Path, u.partSize()); err != nil {
			_ = u.State.MarkFailed(path, err.Error())
			return err
		}
		return nil
	}
	_ = u.State.MarkFailed(path, err.Error())
	return err
}

func (u *Uploader) uploadParts(ctx context.Context, client *dacast.Client, path string, size int64, uploaderID, s3Path string, partSize int64) error {
	totalParts := int((size + partSize - 1) / partSize)
	existing, err := u.State.Parts(path)
	if err != nil {
		return err
	}

	uploadedBytes := int64(0)
	for p := 1; p <= totalParts; p++ {
		if _, ok := existing[p]; !ok {
			continue
		}
		offset := int64(p-1) * partSize
		length := partSize
		if offset+length > size {
			length = size - offset
		}
		uploadedBytes += length
	}
	if u.OnProgress != nil {
		u.OnProgress(path, uploadedBytes, size, len(existing), totalParts)
	}

	for start := 1; start <= totalParts; start += MaxPartsPerSig {
		end := start + MaxPartsPerSig - 1
		if end > totalParts {
			end = totalParts
		}

		need := false
		for p := start; p <= end; p++ {
			if _, ok := existing[p]; !ok {
				need = true
				break
			}
		}
		if !need {
			continue
		}

		u.Log.Infof("requesting signatures parts %d-%d for %s", start, end, filepath.Base(path))
		urls, err := withRetry(ctx, u.Log, "signatures", func() ([]string, error) {
			return client.PresignedURLs(ctx, s3Path, uploaderID, start, end)
		})
		if err != nil {
			return err
		}
		if len(urls) != end-start+1 {
			return fmt.Errorf("expected %d URLs, got %d", end-start+1, len(urls))
		}

		for i, url := range urls {
			partNum := start + i
			if _, ok := existing[partNum]; ok {
				continue
			}
			offset := int64(partNum-1) * partSize
			length := partSize
			if offset+length > size {
				length = size - offset
			}

			etag, err := u.putPartWithRetry(ctx, client, path, url, offset, length, partNum, totalParts)
			if err != nil {
				return err
			}
			if err := u.State.SavePart(path, partNum, etag); err != nil {
				return err
			}
			existing[partNum] = etag
			uploadedBytes += length
			if u.OnProgress != nil {
				u.OnProgress(path, uploadedBytes, size, partNum, totalParts)
			}
			u.Log.Infof("uploaded part %d/%d of %s", partNum, totalParts, filepath.Base(path))
		}
	}

	etags, err := u.State.OrderedETags(path, totalParts)
	if err != nil {
		return err
	}
	u.Log.Infof("completing multipart for %s", filepath.Base(path))
	done, err := withRetry(ctx, u.Log, "complete-multipart", func() (*dacast.CompleteResponse, error) {
		return client.CompleteMultipart(ctx, s3Path, uploaderID, etags)
	})
	if err != nil {
		return err
	}
	if err := u.State.MarkDone(path, done.VodID); err != nil {
		return err
	}
	u.Log.Infof("upload complete %s vod_id=%s", filepath.Base(path), done.VodID)
	return nil
}

func (u *Uploader) putPartWithRetry(ctx context.Context, client *dacast.Client, path, url string, offset, length int64, partNum, totalParts int) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= MaxPartRetries; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		etag, err := u.putPartOnce(ctx, client, path, url, offset, length)
		if err == nil {
			return etag, nil
		}
		lastErr = err
		backoff := time.Duration(math.Min(60, math.Pow(2, float64(attempt-1)))) * time.Second
		jitter := time.Duration(rand.Intn(500)) * time.Millisecond
		u.Log.Warnf("part %d/%d failed (attempt %d/%d): %v; retry in %s", partNum, totalParts, attempt, MaxPartRetries, err, backoff+jitter)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}
	return "", fmt.Errorf("part %d failed after %d retries: %w", partNum, MaxPartRetries, lastErr)
}

func (u *Uploader) putPartOnce(ctx context.Context, client *dacast.Client, path, url string, offset, length int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	section := io.LimitReader(f, length)
	return client.PutPart(ctx, url, section, length)
}

func withRetry[T any](ctx context.Context, log *applog.Logger, name string, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 1; attempt <= MaxAPIRetries; attempt++ {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		v, err := fn()
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !isRetriable(err) {
			return zero, err
		}
		backoff := time.Duration(math.Min(60, math.Pow(2, float64(attempt-1)))) * time.Second
		jitter := time.Duration(rand.Intn(500)) * time.Millisecond
		log.Warnf("%s failed (attempt %d/%d): %v; retry in %s", name, attempt, MaxAPIRetries, err, backoff+jitter)
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}
	return zero, fmt.Errorf("%s failed after %d retries: %w", name, MaxAPIRetries, lastErr)
}

func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "401"), strings.Contains(s, "403"):
		return false
	case strings.Contains(s, "400"), strings.Contains(s, "404"), strings.Contains(s, "422"):
		return false
	default:
		return true
	}
}

func isSessionFatal(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "complete-multipart") ||
		strings.Contains(s, "uploadid") ||
		strings.Contains(s, "no such upload") ||
		strings.Contains(s, "invalid") && strings.Contains(s, "upload")
}
