package source

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	getter "github.com/hashicorp/go-getter/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

const (
	MaxArchiveBytes  int64 = 64 << 20
	MaxExpandedBytes int64 = 256 << 20
	MaxFiles               = 10000
)

// NewHTTPClient configures shared timeouts and redirect policy for source requests.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != schemeHTTP && req.URL.Scheme != schemeHTTPS {
				return fmt.Errorf("unsupported redirect scheme")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != schemeHTTPS {
				return fmt.Errorf("HTTPS downgrade redirect is unsupported")
			}

			return nil
		},
	}
}

// FetchURL verifies the archive before extracting it. Extraction is invoked
// directly so go-getter does not allocate a separate system temporary directory.
func FetchURL(
	ctx context.Context,
	httpClient *http.Client,
	remote, checksum, expectedSHA, workspace, format string,
) (string, string, error) {
	if err := ValidateHTTPURL(remote); err != nil {
		return "", "", err
	}

	u, err := url.Parse(remote)
	if err != nil {
		return "", "", err
	}

	name := path.Base(u.Path)
	if err := ValidatePath(name); err != nil {
		return "", "", fmt.Errorf("download URL requires a filename")
	}

	if err := os.MkdirAll(workspace, 0700); err != nil {
		return "", "", err
	}

	artifact := filepath.Join(workspace, "artifact")
	query := u.Query()
	query.Set("archive", "false")
	if len(checksum) > 0 {
		query.Set("checksum", checksum)
	}
	u.RawQuery = query.Encode()
	client := getter.Client{
		DisableSymlinks: true,
		Getters: []getter.Getter{&getter.HttpGetter{
			Client:                httpClient,
			DoNotCheckHeadFirst:   true,
			MaxBytes:              MaxArchiveBytes,
			ReadTimeout:           httpClient.Timeout,
			XTerraformGetDisabled: true,
		}},
	}
	if _, err := client.Get(
		ctx,
		&getter.Request{
			Src:     u.String(),
			Dst:     artifact,
			GetMode: getter.ModeFile,
		},
	); err != nil {
		return "", "", err
	}

	digest, err := artifactSHA256(artifact)
	if err != nil {
		return "", "", err
	}
	if len(expectedSHA) > 0 && digest != expectedSHA {
		return "", "", fmt.Errorf("download SHA-256 differs from locked artifact")
	}

	root := filepath.Join(workspace, "files")
	if err := os.Mkdir(root, 0700); err != nil {
		return "", "", err
	}

	decompressors := getter.LimitedDecompressors(MaxFiles, MaxExpandedBytes)
	if len(format) == 0 {
		for kind := range decompressors {
			if strings.HasSuffix(strings.ToLower(name), "."+kind) && len(kind) > len(format) {
				format = kind
			}
		}
	}
	if len(format) == 0 {
		if err := os.Rename(artifact, filepath.Join(root, name)); err != nil {
			return "", "", err
		}
	} else {
		if err := validateArchive(ctx, artifact, format); err != nil {
			return "", "", err
		}

		decompressor := decompressors[format]
		if decompressor == nil {
			return "", "", fmt.Errorf("unsupported archive format %q", format)
		}

		destination, directory := root, true
		if format == "gz" || format == "bz2" || format == "xz" || format == "zst" {
			filename := name
			if strings.HasSuffix(strings.ToLower(name), "."+format) {
				filename = name[:len(name)-len(format)-1]
			}
			if err := ValidatePath(filename); err != nil {
				return "", "", err
			}

			destination, directory = filepath.Join(root, filename), false
		}
		if err := decompressor.Decompress(destination, artifact, directory, 0077); err != nil {
			return "", "", err
		}
	}

	if err := ctx.Err(); err != nil {
		return "", "", err
	}

	return root, digest, ValidateTree(ctx, root)
}

func artifactSHA256(filename string) (string, error) {
	// #nosec G304 -- The artifact is created by go-getter in the operation's private workspace.
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(f, MaxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	// go-getter truncates at MaxBytes, so reaching the limit must also fail.
	if size >= MaxArchiveBytes {
		return "", fmt.Errorf("download exceeds size limit")
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}

// ValidateTree rejects links, special files and sources exceeding configured
// size and file-count limits. Git metadata is excluded from the scan.
func ValidateTree(ctx context.Context, root string) error {
	var size int64
	count := 0
	return filepath.WalkDir(root, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source contains a symlink or special file: %s", filename)
		}

		size += info.Size()
		count++
		if size > MaxExpandedBytes || count > MaxFiles {
			return fmt.Errorf("source exceeds size or file-count limit")
		}

		return nil
	})
}

// validateArchive inspects archive entries before go-getter extracts them. Its
// extractors may turn links into regular files, losing their original type.
func validateArchive(ctx context.Context, filename, format string) error {
	if format == "zip" {
		return validateZip(ctx, filename)
	}
	// #nosec G304 -- The downloader supplies an artifact in its private operation workspace.
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	switch format {
	case "tar.gz", "tgz", "gz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer func() {
			_ = gz.Close()
		}()
		reader = gz
	case "tar.bz2", "tbz2", "bz2":
		reader = bzip2.NewReader(f)
	case "tar.xz", "txz", "xz":
		reader, err = xz.NewReader(f)
		if err != nil {
			return err
		}
	case "tar.zst", "tzst", "zst":
		z, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(MaxExpandedBytes)))
		if err != nil {
			return err
		}
		defer z.Close()
		reader = z
	case "tar":
	default:
		return fmt.Errorf("unsupported archive format %q", format)
	}
	if format == "gz" || format == "bz2" || format == "xz" || format == "zst" {
		n, err := io.Copy(io.Discard, io.LimitReader(reader, MaxExpandedBytes+1))
		if n > MaxExpandedBytes {
			return fmt.Errorf("expanded file exceeds limit")
		}

		return err
	}

	tr := tar.NewReader(reader)
	seen := map[string]bool{}
	var total int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if count >= MaxFiles {
			return fmt.Errorf("archive contains too many entries")
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir &&
			h.Typeflag != tar.TypeXGlobalHeader &&
			h.Typeflag != tar.TypeXHeader {
			return fmt.Errorf("archive links and special entries are unsupported: %s", h.Name)
		}

		if err := archiveName(h.Name); err != nil {
			return err
		}
		if h.Typeflag != tar.TypeDir {
			key := strings.ToLower(path.Clean(h.Name))
			if seen[key] {
				return fmt.Errorf("duplicate archive file: %s", h.Name)
			}

			seen[key] = true
		}
		if h.Size < 0 || h.Size > MaxExpandedBytes-total {
			return fmt.Errorf("archive exceeds expanded size limit")
		}

		total += h.Size
	}
}

func archiveName(name string) error {
	if path.IsAbs(name) || strings.ContainsAny(name, "\\:\x00") {
		return fmt.Errorf("unsafe archive path: %s", name)
	}
	if slices.Contains(strings.Split(name, "/"), "..") {
		return fmt.Errorf("archive path escapes source: %s", name)
	}

	return nil
}

func validateZip(ctx context.Context, filename string) error {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer func() {
		_ = z.Close()
	}()

	if len(z.File) > MaxFiles {
		return fmt.Errorf("archive contains too many entries")
	}

	var total uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := archiveName(f.Name); err != nil {
			return err
		}
		if !f.Mode().IsRegular() && !f.Mode().IsDir() {
			return fmt.Errorf("archive links and special entries are unsupported: %s", f.Name)
		}
		if !f.FileInfo().IsDir() {
			key := strings.ToLower(path.Clean(f.Name))
			if seen[key] {
				return fmt.Errorf("duplicate archive file: %s", f.Name)
			}

			seen[key] = true
		}
		if f.UncompressedSize64 > uint64(MaxExpandedBytes)-total {
			return fmt.Errorf("archive exceeds expanded size limit")
		}

		total += f.UncompressedSize64
	}
	return nil
}
