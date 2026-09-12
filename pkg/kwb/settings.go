package kwb

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Settings struct {
	RootPath        string
	IndexPath       string
	ExtraExtensions []string
	ExcludeDirs     []string
	MaxFileSize     int
	BatchSize       int
	IndexType       string
	Rebuild         bool
	SearchTimeout   time.Duration
	SearchLimit     int
}

func NewSettings() *Settings {
	return &Settings{
		RootPath: ".", IndexPath: ".go42x/kwb/index",
		MaxFileSize: 5 * 1024 * 1024, BatchSize: 1000, IndexType: "scorch",
		SearchTimeout: 5 * time.Second, SearchLimit: 10,
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if s.RootPath == "" || s.IndexPath == "" {
		return fmt.Errorf("root and index paths are required")
	}
	if s.BatchSize <= 0 {
		return fmt.Errorf("batch size must be greater than 0")
	}
	if s.MaxFileSize <= 0 {
		return fmt.Errorf("max file size must be greater than 0")
	}
	if s.SearchLimit <= 0 || s.SearchLimit > MaxSearchLimit {
		return fmt.Errorf("search limit must be between 1 and %d", MaxSearchLimit)
	}
	if s.SearchTimeout <= 0 {
		return fmt.Errorf("search timeout must be greater than 0")
	}
	if s.IndexType != "scorch" && s.IndexType != "upsidedown" {
		return fmt.Errorf("invalid index type: %s (must be 'scorch' or 'upsidedown')", s.IndexType)
	}
	return nil
}

func (s *Settings) IndexExists() bool {
	_, err := os.Stat(filepath.Join(s.IndexPath, "CURRENT"))
	return err == nil
}
