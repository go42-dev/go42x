package vendoring

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
