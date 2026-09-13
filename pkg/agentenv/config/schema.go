package config

import _ "embed"

//go:embed go42x.schema.json
var schema string

// Schema returns the JSON schema for go42x.yaml.
func Schema() string {
	return schema
}
