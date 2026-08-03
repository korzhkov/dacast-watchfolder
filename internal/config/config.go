package config

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/ysk/dacast-watchfolder/internal/appdir"
)

type Config struct {
	APIKey      string `json:"api_key"`
	WatchFolder string `json:"watch_folder"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func Load() (*Store, error) {
	path, err := appdir.ConfigPath()
	if err != nil {
		return nil, err
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) Set(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
