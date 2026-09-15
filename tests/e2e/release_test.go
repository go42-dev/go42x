package e2e_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type releaseArtifact struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	Extra  struct {
		Format string `json:"Format"`
	} `json:"extra"`
}

type releaseBundle struct {
	dir      string
	metadata struct {
		Version string `json:"version"`
		Tag     string `json:"tag"`
		Commit  string `json:"commit"`
	}
	artifacts []releaseArtifact
	checksums map[string]string
}

func runReleaseTests(m *testing.M, dist string) int {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve project root:", err)
		return 1
	}
	if !filepath.IsAbs(dist) {
		dist = filepath.Join(root, dist)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	bundle, err := verifyReleaseBundle(ctx, root, dist)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify release:", err)
		return 1
	}
	archive, err := bundle.artifact("Archive", runtime.GOOS, runtime.GOARCH, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "select native release archive:", err)
		return 1
	}
	if runtime.GOOS == "darwin" {
		if err := bundle.verifyHomebrew(ctx, archive); err != nil {
			fmt.Fprintln(os.Stderr, "verify Homebrew cask:", err)
			return 1
		}
	}
	buildDir := filepath.Join(root, ".build")
	if err := os.MkdirAll(buildDir, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir, err := os.MkdirTemp(buildDir, "release-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := extractReleaseArchive(filepath.Join(dist, archive.Name), dir, root); err != nil {
		fmt.Fprintln(os.Stderr, "extract release archive:", err)
		return 1
	}
	binaryPath, binaryVersion = filepath.Join(dir, "go42x"), bundle.metadata.Version
	fmt.Printf("Testing %s (%s) on %s/%s\n", archive.Name, binaryVersion, runtime.GOOS, runtime.GOARCH)
	return m.Run()
}

func verifyReleaseBundle(ctx context.Context, root, dist string) (*releaseBundle, error) {
	bundle := &releaseBundle{dir: dist}
	if err := readReleaseJSON(filepath.Join(dist, "metadata.json"), &bundle.metadata); err != nil {
		return nil, err
	}
	if bundle.metadata.Version == "" || bundle.metadata.Tag == "" || bundle.metadata.Commit == "" {
		return nil, errors.New("metadata.json requires version, tag, and commit")
	}
	git := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	git.Dir = root
	commit, err := git.Output()
	if err != nil {
		return nil, fmt.Errorf("read source commit: %w", err)
	}
	if bundle.metadata.Commit != strings.TrimSpace(string(commit)) {
		return nil, fmt.Errorf(
			"release commit %s does not match source commit %s",
			bundle.metadata.Commit,
			bytes.TrimSpace(commit),
		)
	}
	if err := readReleaseJSON(filepath.Join(dist, "artifacts.json"), &bundle.artifacts); err != nil {
		return nil, err
	}
	if _, err := hashReleaseFile(filepath.Join(dist, "homebrew", "Casks", "go42x.rb")); err != nil {
		return nil, fmt.Errorf("read Homebrew cask: %w", err)
	}
	bundle.checksums, err = verifyReleaseChecksums(dist)
	if err != nil {
		return nil, err
	}
	for _, artifact := range bundle.artifacts {
		switch artifact.Type {
		case "Archive", "Linux Package", "SBOM":
			if _, ok := bundle.checksums[artifact.Name]; !ok {
				return nil, fmt.Errorf("%s is missing from checksums.txt", artifact.Name)
			}
		}
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			archive, err := bundle.artifact("Archive", goos, goarch, "")
			if err != nil {
				return nil, err
			}
			var sbom struct {
				SPDXVersion string            `json:"spdxVersion"`
				Packages    []json.RawMessage `json:"packages"`
			}
			if err := readReleaseJSON(filepath.Join(dist, archive.Name+".sbom.json"), &sbom); err != nil {
				return nil, err
			}
			if sbom.SPDXVersion == "" || len(sbom.Packages) == 0 {
				return nil, fmt.Errorf("%s.sbom.json requires spdxVersion and nonempty packages", archive.Name)
			}
			if goos == "linux" {
				for _, format := range []string{"deb", "rpm", "apk"} {
					if _, err := bundle.artifact("Linux Package", goos, goarch, format); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return bundle, nil
}

func (b *releaseBundle) artifact(kind, goos, goarch, format string) (releaseArtifact, error) {
	var found releaseArtifact
	count := 0
	for _, artifact := range b.artifacts {
		if artifact.Type == kind && artifact.GOOS == goos && artifact.GOARCH == goarch &&
			(format == "" || artifact.Extra.Format == format) {
			found = artifact
			count++
		}
	}
	if count != 1 {
		return releaseArtifact{}, fmt.Errorf(
			"expected one %s %s for %s/%s, found %d",
			kind,
			format,
			goos,
			goarch,
			count,
		)
	}
	return found, nil
}

func readReleaseJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func hashReleaseFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("%s must be a nonempty regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyReleaseChecksums(dist string) (map[string]string, error) {
	file, err := os.Open(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if len(text) < 67 || text[64] != ' ' || (text[65] != ' ' && text[65] != '*') {
			return nil, fmt.Errorf("invalid checksum entry on line %d", line)
		}
		expected, name := text[:64], text[66:]
		if _, err := hex.DecodeString(expected); err != nil {
			return nil, fmt.Errorf("invalid SHA-256 on line %d: %w", line, err)
		}
		if !filepath.IsLocal(name) || filepath.Base(name) != name {
			return nil, fmt.Errorf("invalid release artifact name %q", name)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum for %s", name)
		}
		actual, err := hashReleaseFile(filepath.Join(dist, name))
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(actual, expected) {
			return nil, fmt.Errorf("SHA-256 mismatch for %s: got %s, expected %s", name, actual, expected)
		}
		checksums[name] = actual
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums.txt: %w", err)
	}
	if len(checksums) == 0 {
		return nil, errors.New("checksums.txt is empty")
	}
	return checksums, nil
}

func (b *releaseBundle) verifyHomebrew(ctx context.Context, archive releaseArtifact) error {
	// Use Homebrew's parser without installing a tap or fetching snapshot URLs.
	const script = `
require "cask/cask_loader"
require "json"
cask = Cask::CaskLoader::FromContentLoader.new(File.read(ARGV.fetch(0))).load(config: nil)
puts JSON.generate(casks: [cask.to_h])
`
	command := exec.CommandContext(ctx, "brew", "ruby", "-e", script, "--",
		filepath.Join(b.dir, "homebrew", "Casks", "go42x.rb"))
	command.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1")
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("parse cask with Homebrew: %w", err)
	}
	var result struct {
		Casks []struct {
			Token     string `json:"token"`
			Version   string `json:"version"`
			URL       string `json:"url"`
			SHA256    string `json:"sha256"`
			Artifacts []struct {
				Binary []json.RawMessage `json:"binary"`
			} `json:"artifacts"`
		} `json:"casks"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return fmt.Errorf("decode Homebrew output: %w", err)
	}
	if len(result.Casks) != 1 {
		return fmt.Errorf("expected one Homebrew cask, found %d", len(result.Casks))
	}
	cask := result.Casks[0]
	url := "https://github.com/go42-dev/go42x/releases/download/" + b.metadata.Tag + "/" + archive.Name
	if cask.Token != "go42x" || cask.Version != b.metadata.Version ||
		cask.URL != url || cask.SHA256 != b.checksums[archive.Name] {
		return fmt.Errorf("cask token, version, URL, or checksum does not match %s", archive.Name)
	}
	for _, artifact := range cask.Artifacts {
		var binary string
		if len(artifact.Binary) > 0 && json.Unmarshal(artifact.Binary[0], &binary) == nil && binary == "go42x" {
			return nil
		}
	}
	return errors.New("cask does not install the go42x binary")
}

func extractReleaseArchive(path, dir, root string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = compressed.Close() }()
	archive := tar.NewReader(compressed)
	found := make(map[string]bool)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		// Extract only these fixed names; ignore other members without writing them.
		switch header.Name {
		case "go42x", "LICENSE", "README.md":
		default:
			continue
		}
		if found[header.Name] || header.Typeflag != tar.TypeReg || header.Size == 0 {
			return fmt.Errorf("%s must appear once as a nonempty regular file", header.Name)
		}
		found[header.Name] = true
		mode := os.FileMode(0600)
		if header.Name == "go42x" {
			if header.Mode&0111 == 0 {
				return errors.New("go42x is not executable in the release archive")
			}
			mode = 0700
		}
		destination, err := os.OpenFile(filepath.Join(dir, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(destination, archive, header.Size)
		if err := errors.Join(copyErr, destination.Close()); err != nil {
			return fmt.Errorf("extract %s: %w", header.Name, err)
		}
	}
	// Reading to gzip EOF also verifies its trailer after tar's end marker.
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		return fmt.Errorf("finish gzip archive: %w", err)
	}
	for _, name := range []string{"go42x", "LICENSE", "README.md"} {
		if !found[name] {
			return fmt.Errorf("release archive is missing %s", name)
		}
		if name == "go42x" {
			continue
		}
		expected, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected) {
			return fmt.Errorf("packaged %s differs from the source file", name)
		}
	}
	return nil
}
