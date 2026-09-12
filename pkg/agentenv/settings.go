package agentenv

import "fmt"

type Settings struct {
	OutputPath string

	GenerateClean bool
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	return nil
}
