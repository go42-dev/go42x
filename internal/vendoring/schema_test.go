package vendoring

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func compileVendorSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "schemas", "vendor.schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("vendor.schema.yaml", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("vendor.schema.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func assertVendorSchema(t *testing.T, compiled *jsonschema.Schema, data string, wantValid bool) {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal([]byte(data), &document); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(document); (err == nil) != wantValid {
		t.Errorf("schema validation = %v, want valid %v", err, wantValid)
	}
}

func TestSchemaDefaultManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := Touch(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	compiled := compileVendorSchema(t)
	assertVendorSchema(t, compiled, string(data), true)
	bundled, err := os.ReadFile(filepath.Join("..", "..", "assets", "agentenv", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	assertVendorSchema(t, compiled, string(bundled), true)
}

func TestSchemaSourceVariants(t *testing.T) {
	compiled := compileVendorSchema(t)
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{
			"HTTP",
			"{type: http, url: https://example.com/file}",
			true,
		},
		{
			"Git",
			"{type: git, url: https://example.com/repository.git, ref: main}",
			true,
		},
		{
			"npm",
			"{type: npm, package: '@fixture/assets', version: latest}",
			true,
		},
		{
			"Git requires ref",
			"{type: git, url: https://example.com/repository.git}",
			false,
		},
		{
			"unsupported source",
			"{type: ftp, url: https://example.com/file}",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := fmt.Sprintf(
				"version: '1.0'\ndependencies:\n  assets:\n    source: %s\n    files: [{from: file, to: file}]\n",
				tc.source,
			)
			assertVendorSchema(t, compiled, data, tc.valid)
		})
	}
}
