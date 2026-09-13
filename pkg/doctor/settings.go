package doctor

import (
	"fmt"
	"time"
)

type Settings struct {
	RootPath  string
	IndexPath string
	ProbeMCP  bool
	Timeout   time.Duration
	JSON      bool
}

func NewSettings() *Settings {
	return &Settings{
		RootPath: ".",
		Timeout:  10 * time.Second,
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if s.RootPath == "" {
		return fmt.Errorf("project root is required")
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	return nil
}
