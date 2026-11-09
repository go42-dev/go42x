package vendoring

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type converterFunc func([]byte) ([]byte, error)

func (f converterFunc) Convert(data []byte) ([]byte, error) {
	return f(data)
}

func TestSelectFileConverter(t *testing.T) {
	root := t.TempDir()
	original := []byte("<h1>Original</h1>\n\n")
	if err := os.WriteFile(filepath.Join(root, "source.html"), original, 0600); err != nil {
		t.Fatal(err)
	}
	conversionError := errors.New("conversion failed")
	for _, tc := range []struct {
		name      string
		converter string
		want      []byte
		err       error
	}{
		{
			name: "copy",
			want: original,
		},
		{
			name:      "registered converter",
			converter: "fixture",
			want:      []byte("custom conversion"),
		},
		{
			name:      "conversion error",
			converter: "fixture",
			err:       conversionError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := &Service{converters: map[string]converterAccessor{
				"fixture": converterFunc(func(data []byte) ([]byte, error) {
					calls++
					if !bytes.Equal(data, original) {
						t.Fatal("converter received altered source bytes")
					}
					return tc.want, tc.err
				}),
			}}
			data, err := s.selectFile(root, FileMapping{
				From:      "source.html",
				Converter: tc.converter,
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("selectFile() error = %v, want %v", err, tc.err)
			}
			if tc.err == nil && !bytes.Equal(data, tc.want) {
				t.Fatalf("selectFile() = %q, want %q", data, tc.want)
			}
			wantCalls := 1

			if len(tc.converter) == 0 {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("converter called %d times, want %d", calls, wantCalls)
			}
		})
	}
}

func TestSelectDirectory(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"skills/review/SKILL.md":         "skill",
		"skills/review/scripts/check.sh": "script",
		"skills/review/.hidden":          "hidden",
		"skills/review/.git/config":      "[core]",
		"skills/empty/.git/config":       "[core]",
		"skills/file.md":                 "file",
	} {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := selectDirectory(root, DirectoryMapping{
		From: "skills/review",
		To:   "vendor/review",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"vendor/review/SKILL.md":         "skill",
		"vendor/review/scripts/check.sh": "script",
		"vendor/review/.hidden":          "hidden",
	}
	if len(files) != len(want) {
		t.Fatalf("selectDirectory() = %v, want %v", files, want)
	}
	for name, content := range want {
		if string(files[name]) != content {
			t.Errorf("%s = %q, want %q", name, files[name], content)
		}
	}

	if err := os.Symlink(
		filepath.Join(root, "skills/file.md"),
		filepath.Join(root, "skills/review/link.md"),
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "skills/review"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	for from, wantErr := range map[string]string{
		"skills/review":  "expected a regular file",
		"linked":         "symlink is unsupported",
		"skills/empty":   "directory has no files",
		"skills/file.md": "expected a directory",
		"skills/missing": "no such file",
	} {
		t.Run(from, func(t *testing.T) {
			_, err := selectDirectory(root, DirectoryMapping{
				From: from,
				To:   "vendor",
			})
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("selectDirectory(%s) error = %v, want %q", from, err, wantErr)
			}
		})
	}
}

func TestSelectDirectoryLimitsFiles(t *testing.T) {
	root := t.TempDir()
	for i := range maxDirectoryFiles + 1 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := selectDirectory(filepath.Dir(root), DirectoryMapping{
		From: filepath.Base(root),
		To:   "vendor",
	})
	if err == nil || !strings.Contains(err.Error(), "more than 1000 files") {
		t.Fatalf("selectDirectory() error = %v, want the file limit", err)
	}
}
