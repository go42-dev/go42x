package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDockerPublicationRequiresCallerCommit(t *testing.T) {
	script := dockerWorkflowScript(t, "140-docker-build.yaml", "docker-build", "Validate image publication target")
	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	for _, tt := range []struct {
		name    string
		ci      bool
		changes map[string]string
		valid   bool
	}{
		{
			name:  "release commit",
			valid: true,
		},
		{
			name:    "prerelease",
			changes: map[string]string{"IMAGE_TAG": "v1.2.3-rc.1"},
			valid:   true,
		},
		{
			name:    "release branch",
			changes: map[string]string{"EVENT_REF": "refs/heads/feature"},
		},
		{
			name:    "release pull request",
			changes: map[string]string{"EVENT_NAME": "pull_request"},
		},
		{
			name: "release from CI",
			changes: map[string]string{
				"CALLER_WORKFLOW_REF": "go42-dev/go42x/.github/workflows/100-unified-workflow.yaml@refs/heads/master",
			},
		},
		{
			name:    "different workflow commit",
			changes: map[string]string{"CALLER_WORKFLOW_SHA": other},
		},
		{
			name:    "different event commit",
			changes: map[string]string{"EVENT_SHA": other},
		},
		{
			name:    "invalid source",
			changes: map[string]string{"SOURCE_SHA": "master"},
		},
		{
			name:    "empty source",
			changes: map[string]string{"SOURCE_SHA": ""},
		},
		{
			name:    "empty tag",
			changes: map[string]string{"IMAGE_TAG": ""},
		},
		{
			name:    "development tag",
			changes: map[string]string{"IMAGE_TAG": "dev"},
		},
		{
			name:    "latest tag",
			changes: map[string]string{"IMAGE_TAG": "latest"},
		},
		{
			name:    "invalid tag",
			changes: map[string]string{"IMAGE_TAG": "v1.2.3; echo unexpected"},
		},
		{
			name:    "invalid package",
			changes: map[string]string{"SERVICE_NAME": "../other"},
		},
		{
			name:  "CI push",
			ci:    true,
			valid: true,
		},
		{
			name:    "CI manual",
			ci:      true,
			changes: map[string]string{"EVENT_NAME": "workflow_dispatch"},
			valid:   true,
		},
		{
			name: "CI pull request",
			ci:   true,
			changes: map[string]string{
				"EVENT_NAME":          "pull_request",
				"EVENT_REF":           "refs/pull/42/merge",
				"CALLER_WORKFLOW_REF": "go42-dev/go42x/.github/workflows/100-unified-workflow.yaml@refs/pull/42/merge",
			},
			valid: true,
		},
		{
			name:    "CI different run",
			ci:      true,
			changes: map[string]string{"IMAGE_TAG": "ci-124-1"},
		},
		{
			name:    "CI different attempt",
			ci:      true,
			changes: map[string]string{"IMAGE_TAG": "ci-123-2"},
		},
		{
			name:    "CI different source",
			ci:      true,
			changes: map[string]string{"SOURCE_SHA": other},
		},
		{
			name: "CI different workflow",
			ci:   true,
			changes: map[string]string{
				"CALLER_WORKFLOW_REF": "go42-dev/go42x/.github/workflows/300-release.yaml@refs/heads/master",
			},
		},
		{
			name:    "CI different ref",
			ci:      true,
			changes: map[string]string{"EVENT_REF": "refs/heads/feature"},
		},
		{
			name:    "CI unsupported event",
			ci:      true,
			changes: map[string]string{"EVENT_NAME": "pull_request_target"},
		},
		{
			name:    "CI latest tag",
			ci:      true,
			changes: map[string]string{"IMAGE_TAG": "latest"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := map[string]string{
				"REPOSITORY":          "go42-dev/go42x",
				"SERVICE_NAME":        "go42x",
				"IMAGE_TAG":           "v1.2.3",
				"SOURCE_SHA":          sha,
				"EVENT_NAME":          "workflow_dispatch",
				"EVENT_REF":           "refs/heads/master",
				"EVENT_SHA":           sha,
				"CALLER_WORKFLOW_REF": "go42-dev/go42x/.github/workflows/300-release.yaml@refs/heads/master",
				"CALLER_WORKFLOW_SHA": sha,
				"RUN_ID":              "123",
				"RUN_ATTEMPT":         "1",
			}
			if tt.ci {
				environment["IMAGE_TAG"] = "ci-123-1"
				environment["EVENT_NAME"] = "push"
				environment["CALLER_WORKFLOW_REF"] = "go42-dev/go42x/.github/workflows/100-unified-workflow.yaml@refs/heads/master"
			}
			for key, value := range tt.changes {
				environment[key] = value
			}

			command := exec.CommandContext(t.Context(), "bash", "-c", script)
			for key, value := range environment {
				command.Env = append(command.Env, key+"="+value)
			}
			output, err := command.CombinedOutput()
			if (err == nil) != tt.valid {
				t.Fatalf("publication guard: %v\n%s", err, output)
			}
		})
	}
}

func TestDockerCleanupProtectsOtherTags(t *testing.T) {
	script := dockerWorkflowScript(t, "160-cleanup.yaml", "cleanup", "Delete temporary Docker image")
	version := dockerRegistryVersion(1, "2026-01-01T00:00:00Z", "ci-123-1")
	for _, tt := range []struct {
		name        string
		tag         string
		versions    []map[string]any
		current     map[string]any
		deleteError string
		valid       bool
		deleted     bool
		warning     bool
	}{
		{
			name:     "exact CI tag",
			tag:      "ci-123-1",
			versions: []map[string]any{version},
			current:  version,
			valid:    true,
			deleted:  true,
		},
		{
			name:        "last tagged version",
			tag:         "ci-123-1",
			versions:    []map[string]any{version},
			current:     version,
			deleteError: "gh: You cannot delete the last tagged version of a package. You must delete the package instead. (HTTP 400)",
			valid:       true,
			warning:     true,
		},
		{
			name:        "other bad request",
			tag:         "ci-123-1",
			versions:    []map[string]any{version},
			current:     version,
			deleteError: "gh: Invalid package version (HTTP 400)",
		},
		{
			name:        "permission denied",
			tag:         "ci-123-1",
			versions:    []map[string]any{version},
			current:     version,
			deleteError: "gh: Resource not accessible by integration (HTTP 403)",
		},
		{
			name:     "already absent",
			tag:      "ci-123-1",
			versions: []map[string]any{},
			valid:    true,
		},
		{
			name:     "release tag",
			tag:      "v1.2.3",
			versions: []map[string]any{version},
		},
		{
			name:     "latest tag",
			tag:      "latest",
			versions: []map[string]any{version},
		},
		{
			name: "shared release",
			tag:  "ci-123-1",
			versions: []map[string]any{
				dockerRegistryVersion(1, "2026-01-01T00:00:00Z", "ci-123-1", "v1.2.3"),
			},
		},
		{
			name:     "duplicate matches",
			tag:      "ci-123-1",
			versions: []map[string]any{version, version},
		},
		{
			name:     "concurrent retag",
			tag:      "ci-123-1",
			versions: []map[string]any{version},
			current:  dockerRegistryVersion(1, "2026-01-01T00:00:00Z", "ci-123-1", "latest"),
		},
		{
			name:     "changed digest",
			tag:      "ci-123-1",
			versions: []map[string]any{version},
			current:  dockerRegistryVersion(2, "2026-01-01T00:00:00Z", "ci-123-1"),
		},
		{
			name:     "malformed response",
			tag:      "ci-123-1",
			versions: []map[string]any{{"id": 1}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := dockerRegistryFixture(t, tt.versions, tt.current)
			output, err := runDockerCleanup(t, dir, script, map[string]string{
				"TARGET_TAG":   tt.tag,
				"DELETE_ERROR": tt.deleteError,
			})
			if (err == nil) != tt.valid {
				t.Fatalf("cleanup: %v\n%s", err, output)
			}
			deleted, err := os.ReadFile(filepath.Join(dir, "deleted"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if (len(deleted) > 0) != tt.deleted {
				t.Fatalf("unexpected deletions: %s\n%s", deleted, output)
			}
			if strings.Contains(string(output), "::warning::Retaining CI tag") != tt.warning {
				t.Fatalf("unexpected cleanup warning: %s", output)
			}
			if len(tt.deleteError) > 0 && !tt.warning && !strings.Contains(string(output), tt.deleteError) {
				t.Fatalf("cleanup hid the API error: %s", output)
			}
		})
	}
}

func TestDockerRegistryRetention(t *testing.T) {
	script := dockerWorkflowScript(t, "400-cleanup-registry.yaml", "cleanup-registry", "Cleanup old CI images")
	old := dockerRegistryVersion(1, "2025-01-01T00:00:00Z", "ci-123-1")
	versions := []map[string]any{
		old,
		dockerRegistryVersion(2, "2026-02-01T00:00:00Z", "ci-124-1"),
		dockerRegistryVersion(3, "2026-03-01T00:00:00Z", "ci-125-1"),
		dockerRegistryVersion(4, "2025-01-01T00:00:00Z", "v1.2.3"),
		dockerRegistryVersion(5, "2025-01-01T00:00:00Z", "ci-120-1", "latest"),
		dockerRegistryVersion(6, "2025-01-01T00:00:00Z"),
	}
	for _, tt := range []struct {
		name    string
		dryRun  string
		keep    string
		current map[string]any
		valid   bool
		deleted bool
	}{
		{
			name:    "old CI only",
			dryRun:  "false",
			keep:    "1",
			current: old,
			valid:   true,
			deleted: true,
		},
		{
			name:   "dry run",
			dryRun: "true",
			keep:   "1",
			valid:  true,
		},
		{
			name:   "keep count",
			dryRun: "false",
			keep:   "3",
			valid:  true,
		},
		{
			name:    "concurrent retag",
			dryRun:  "false",
			keep:    "1",
			current: dockerRegistryVersion(1, "2025-01-01T00:00:00Z", "ci-123-1", "v1.2.3"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := dockerRegistryFixture(t, versions, tt.current)
			output, err := runDockerCleanup(t, dir, script, map[string]string{
				"DRY_RUN":        tt.dryRun,
				"KEEP_VERSIONS":  tt.keep,
				"RETENTION_DAYS": "30",
				"DEBUG_MODE":     "false",
			})
			if (err == nil) != tt.valid {
				t.Fatalf("retention cleanup: %v\n%s", err, output)
			}
			deleted, err := os.ReadFile(filepath.Join(dir, "deleted"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			want := ""
			if tt.deleted {
				want = "orgs/go42-dev/packages/container/go42x/versions/1\n"
			}
			if string(deleted) != want {
				t.Fatalf("deleted %q, want %q\n%s", deleted, want, output)
			}
		})
	}
}

func TestDockerScanInputs(t *testing.T) {
	script := dockerWorkflowScript(
		t,
		"140-security-extra.yaml",
		"trivy",
		"Validate image reference",
	)
	reference := "ghcr.io/go42-dev/go42x@sha256:" + strings.Repeat("a", 64)
	for _, tt := range []struct {
		name      string
		reference string
		valid     bool
	}{
		{
			name:      "immutable digest",
			reference: reference,
			valid:     true,
		},
		{
			name:      "mutable tag",
			reference: "ghcr.io/go42-dev/go42x:latest",
		},
		{
			name:      "different registry",
			reference: "docker.io/go42-dev/go42x@sha256:" + strings.Repeat("a", 64),
		},
		{
			name:      "short digest",
			reference: strings.TrimSuffix(reference, "a"),
		},
		{
			name:      "invalid digest",
			reference: "ghcr.io/go42-dev/go42x@sha256:" + strings.Repeat("g", 64),
		},
		{
			name: "empty reference",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "bash", "-c", script)
			command.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"IMAGE_REFERENCE=" + tt.reference,
			}
			output, err := command.CombinedOutput()
			if (err == nil) != tt.valid {
				t.Fatalf("scan input validation: %v\n%s", err, output)
			}
		})
	}
}

func TestDockerScanArchitectures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../..", ".github/workflows/140-security-extra.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Arch []string `yaml:"arch"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Env map[string]string `yaml:"env"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}

	scan := workflow.Jobs["trivy"]
	if !slices.Equal(scan.Strategy.Matrix.Arch, []string{"amd64", "arm64"}) {
		t.Fatalf("scan architectures = %v, want amd64 and arm64", scan.Strategy.Matrix.Arch)
	}
	if scan.Env["TRIVY_PLATFORM"] != "linux/${{ matrix.arch }}" {
		t.Fatalf("scan platform = %q, want the matrix architecture", scan.Env["TRIVY_PLATFORM"])
	}
}

func TestDockerLatestPromotion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../..", ".github/workflows/140-docker-build.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				If   string `yaml:"if"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var condition string
	for _, step := range workflow.Jobs["docker-build"].Steps {
		if step.Name == "Publish stable image as latest" {
			condition = step.If
			break
		}
	}
	const promotionCondition = "startsWith(inputs.image_tag, 'v') && !contains(inputs.image_tag, '-')"
	if condition != promotionCondition {
		t.Fatalf("latest promotion must require a successful stable release: %s", condition)
	}

	script := dockerWorkflowScript(t, "140-docker-build.yaml", "docker-build", "Publish stable image as latest")
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tt := range []struct {
		name   string
		latest string
		valid  bool
	}{
		{
			name:   "same manifest",
			latest: digest,
			valid:  true,
		},
		{
			name:   "different manifest",
			latest: "sha256:" + strings.Repeat("b", 64),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mock := `#!/bin/bash
set -euo pipefail
case "$*" in
  'buildx imagetools create --tag ghcr.io/go42-dev/go42x:latest ghcr.io/go42-dev/go42x@'*) ;;
  'buildx imagetools inspect --format {{.Manifest.Digest}} ghcr.io/go42-dev/go42x:latest') echo "$MOCK_LATEST_DIGEST" ;;
  *) echo "Unexpected Docker command: $*" >&2; exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(mock), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "bash", "-c", script)
			command.Env = []string{
				"PATH=" + dir,
				"IMAGE_NAME=ghcr.io/go42-dev/go42x",
				"IMAGE_REFERENCE=ghcr.io/go42-dev/go42x@" + digest,
				"IMAGE_DIGEST=" + digest,
				"MOCK_LATEST_DIGEST=" + tt.latest,
				"GITHUB_STEP_SUMMARY=" + filepath.Join(dir, "summary"),
			}
			output, err := command.CombinedOutput()
			if (err == nil) != tt.valid {
				t.Fatalf("latest promotion: %v\n%s", err, output)
			}
		})
	}
}

func dockerWorkflowScript(t *testing.T, file, job, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("../..", ".github/workflows", file))
	if err != nil {
		t.Fatal(err)
	}
	var workflow compiledWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, step := range workflow.Jobs[job].Steps {
		if step.Name == name && len(step.Run) > 0 {
			return step.Run
		}
	}
	t.Fatalf("missing script %s in %s/%s", name, file, job)
	return ""
}

func dockerRegistryVersion(id int, created string, tags ...string) map[string]any {
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id":         id,
		"name":       "sha256:" + strings.Repeat(string(rune('a'+id)), 64),
		"created_at": created,
		"metadata": map[string]any{
			"container": map[string]any{"tags": tags},
		},
	}
}

func dockerRegistryFixture(t *testing.T, versions []map[string]any, current map[string]any) string {
	t.Helper()

	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("Docker cleanup tests require jq")
	}
	dir := t.TempDir()
	for name, value := range map[string]any{"versions.json": versions, "current.json": current} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Every API call stays inside the fixture, including DELETE. The real gh
	// executable is never invoked and no registry credentials are supplied.
	for name, script := range map[string]string{
		"gh": `#!/bin/bash
set -euo pipefail
case "$*" in
  'api users/go42-dev --jq .type') echo Organization ;;
  'api --paginate orgs/go42-dev/packages/container/go42x/versions?per_page=100') cat "$FIXTURE/versions.json" ;;
  'api orgs/go42-dev/packages/container/go42x/versions/'*) cat "$FIXTURE/current.json" ;;
  'api --method DELETE orgs/go42-dev/packages/container/go42x/versions/'*)
    if [ -n "${DELETE_ERROR:-}" ]; then
      printf '%s\n' "$DELETE_ERROR" >&2
      exit 1
    fi
    echo "$4" >> "$FIXTURE/deleted"
    ;;
  *) echo "Unexpected API request: $*" >&2; exit 1 ;;
esac
`,
		"date": "#!/bin/bash\necho '2026-01-01T00:00:00Z'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runDockerCleanup(t *testing.T, dir, script string, environment map[string]string) ([]byte, error) {
	t.Helper()

	command := exec.CommandContext(t.Context(), "bash", "-c", script)
	command.Env = []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FIXTURE=" + dir,
		"OWNER=go42-dev",
		"PACKAGE_NAME=go42x",
		"GITHUB_OUTPUT=" + filepath.Join(dir, "output"),
	}
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	return command.CombinedOutput()
}
