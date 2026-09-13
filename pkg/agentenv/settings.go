package agentenv

import "fmt"

type Settings struct {
	Clean bool
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	return nil
}
