package go42x

import (
	"fmt"
)

type Settings struct {
	Dummy bool
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("options cannot be nil")
	}
	return nil
}
