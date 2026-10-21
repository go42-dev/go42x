package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectZIPLink(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0600)
	f, err := z.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("../escape")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(filename, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateArchive(t.Context(), filename, "zip"); err == nil {
		t.Fatal("ZIP symlink accepted")
	}
}

func TestExpandedArchiveLimit(t *testing.T) {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{
		Name:     "huge",
		Size:     MaxExpandedBytes + 1,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	// The header alone is sufficient to reject an oversized entry.
	filename := filepath.Join(t.TempDir(), "source.tar")
	if err := os.WriteFile(filename, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateArchive(t.Context(), filename, "tar"); err == nil {
		t.Fatal("oversized archive accepted")
	}
}
