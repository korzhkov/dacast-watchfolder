package applog

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ysk/dacast-watchfolder/internal/appdir"
)

const maxBytes = 10 * 1024 * 1024
const uiTailLines = 500

type Logger struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	listeners []func(string)
	lines    []string
}

func Open() (*Logger, error) {
	path, err := appdir.LogPath()
	if err != nil {
		return nil, err
	}
	if err := rotateIfNeeded(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f, path: path, lines: make([]string, 0, uiTailLines)}, nil
}

func rotateIfNeeded(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	bak := path + ".1"
	_ = os.Remove(bak)
	return os.Rename(path, bak)
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Logger) OnLine(fn func(string)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listeners = append(l.listeners, fn)
}

func (l *Logger) Tail() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.lines))
	copy(out, l.lines)
	return out
}

func (l *Logger) Infof(format string, args ...any) {
	l.log("INFO", format, args...)
}

func (l *Logger) Errorf(format string, args ...any) {
	l.log("ERROR", format, args...)
}

func (l *Logger) Warnf(format string, args ...any) {
	l.log("WARN", format, args...)
}

func (l *Logger) log(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s [%s] %s", time.Now().Format("2006-01-02 15:04:05"), level, msg)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_, _ = io.WriteString(l.file, line+"\n")
	}
	l.lines = append(l.lines, line)
	if len(l.lines) > uiTailLines {
		l.lines = l.lines[len(l.lines)-uiTailLines:]
	}
	for _, fn := range l.listeners {
		fn(line)
	}
}
