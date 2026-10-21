package integration_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

func tarFixture(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, h := range headers {
		if err := w.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRejectUnsafeArchivesBeforeExtraction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []tar.Header
	}{
		{
			"traversal",
			[]tar.Header{{
				Name:     "../escape",
				Typeflag: tar.TypeReg,
			}},
		},
		{
			"absolute",
			[]tar.Header{{
				Name:     "/escape",
				Typeflag: tar.TypeReg,
			}},
		},
		{
			"symlink",
			[]tar.Header{{
				Name:     "link",
				Typeflag: tar.TypeSymlink,
				Linkname: "file",
			}},
		},
		{
			"hardlink",
			[]tar.Header{{
				Name:     "link",
				Typeflag: tar.TypeLink,
				Linkname: "file",
			}},
		},
		{
			"fifo",
			[]tar.Header{{
				Name:     "fifo",
				Typeflag: tar.TypeFifo,
			}},
		},
		{
			"case collision",
			[]tar.Header{{
				Name:     "file",
				Typeflag: tar.TypeReg,
			}, {
				Name:     "FILE",
				Typeflag: tar.TypeReg,
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tarFixture(t, tc.headers...)
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(data)
				}),
			)
			defer server.Close()

			workspace := t.TempDir()
			if _, _, err := source.FetchURL(
				t.Context(), source.NewHTTPClient(time.Second), server.URL+"/source.tar", "", "", workspace, "",
			); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			entries, err := os.ReadDir(filepath.Join(workspace, "files"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("archive was extracted before validation: %v %v", entries, err)
			}
		})
	}
}
func TestCompressedFilesRetainURLBasename(t *testing.T) {
	for _, format := range []string{"gz", "xz", "zst"} {
		t.Run(format, func(t *testing.T) {
			var b bytes.Buffer
			switch format {
			case "gz":
				w := gzip.NewWriter(&b)
				if _, err := w.Write([]byte("content")); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			case "xz":
				w, err := xz.NewWriter(&b)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte("content")); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			case "zst":
				w, err := zstd.NewWriter(&b, zstd.WithEncoderConcurrency(1))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte("content")); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(b.Bytes())
				}),
			)
			defer server.Close()

			root, hash, err := source.FetchURL(
				t.Context(), source.NewHTTPClient(time.Second), server.URL+"/file.txt."+format, "", "", t.TempDir(), "",
			)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(root, "file.txt"))
			if err != nil || string(content) != "content" || hash != fmt.Sprintf("%x", sha256.Sum256(b.Bytes())) {
				t.Fatalf("compressed file: %s %s %v", content, hash, err)
			}
		})
	}
}
