package fspatch

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const testManagedBlock = "# go42x -- START\nnew-rule/\n# go42x -- END\n"

func TestWriteManagedBlock(t *testing.T) {
	for _, tt := range []struct {
		name, previous, template, want string
	}{
		{
			name:     "missing file",
			template: "# Header\n\n" + testManagedBlock,
			want:     "# Header\n\n" + testManagedBlock,
		},
		{
			name:     "existing header",
			previous: "# Header\n\n# go42x -- START\nobsolete\n# go42x -- END\ncustom/\n",
			template: "# Header\n\n" + testManagedBlock,
			want:     "# Header\n\n" + testManagedBlock + "custom/\n",
		},
		{
			name:     "legacy header without markers",
			previous: "# Header\n\ncustom/\n",
			template: "# Header\n\n" + testManagedBlock,
			want:     "# Header\n\ncustom/\n\n" + testManagedBlock,
		},
		{
			name:     "unmarked file",
			previous: "user-entry\n",
			template: testManagedBlock,
			want:     "user-entry\n\n" + testManagedBlock,
		},
		{
			name:     "no trailing newline",
			previous: "user-entry",
			template: testManagedBlock,
			want:     "user-entry\n\n" + testManagedBlock,
		},
		{
			name:     "preserve custom rules",
			previous: "before\n# go42x -- START\nobsolete\n# go42x -- END\n!new-rule/keep.md\n",
			template: testManagedBlock,
			want:     "before\n" + testManagedBlock + "!new-rule/keep.md\n",
		},
		{
			name:     "block at EOF",
			previous: "# go42x -- START\nobsolete\n# go42x -- END",
			template: testManagedBlock,
			want:     testManagedBlock,
		},
		{
			name:     "preserve CRLF outside block",
			previous: "before\r\n# go42x -- START\r\nobsolete\r\n# go42x -- END\r\nafter\r\n",
			template: testManagedBlock,
			want:     "before\r\n" + testManagedBlock + "after\r\n",
		},
		{
			name:     "template footer is not managed",
			previous: "before\n# go42x -- START\nobsolete\n# go42x -- END\nafter\n",
			template: "# Header\n" + testManagedBlock + "# Footer\n",
			want:     "before\n" + testManagedBlock + "after\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "ignore")
			if len(tt.previous) > 0 {
				if err := os.WriteFile(path, []byte(tt.previous), 0644); err != nil {
					t.Fatal(err)
				}
			}

			for range 2 {
				plan := NewPlan(slog.New(slog.DiscardHandler), root, t.TempDir())
				if err := plan.WriteManagedBlock(path, tt.template, Sources); err != nil {
					t.Fatal(err)
				}
				if err := plan.Apply(); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != tt.want {
					t.Fatalf("managed block = %q, %v; want %q", got, err, tt.want)
				}
			}
		})
	}
}

func TestWriteManagedBlockRejectsMalformedMarkers(t *testing.T) {
	for _, input := range []string{"previous", "template"} {
		t.Run(input, func(t *testing.T) {
			for _, malformed := range []string{
				"before\n# go42x -- START\nuser rules\n",
				"before\n# go42x -- END\nafter\n",
				"# go42x -- END\n# go42x -- START\n",
				"# go42x -- START\n# go42x -- START\n# go42x -- END\n",
				"# go42x -- START\n# go42x -- END\n# go42x -- END\n",
				testManagedBlock + testManagedBlock,
			} {
				t.Run(malformed, func(t *testing.T) {
					root, backups := t.TempDir(), t.TempDir()
					path := filepath.Join(root, "ignore")
					previous, template := malformed, testManagedBlock
					if input == "template" {
						previous, template = testManagedBlock, malformed
					}
					if err := os.WriteFile(path, []byte(previous), 0644); err != nil {
						t.Fatal(err)
					}
					plan := NewPlan(slog.New(slog.DiscardHandler), root, backups)

					if err := plan.WriteManagedBlock(path, template, Sources); err == nil {
						t.Fatal("malformed markers accepted")
					}
					if err := plan.Apply(); err != nil {
						t.Fatal(err)
					}
					got, err := os.ReadFile(path)
					if err != nil || string(got) != previous {
						t.Fatalf("invalid block changed file: %q, %v", got, err)
					}
					entries, err := os.ReadDir(backups)
					if err != nil || len(entries) != 0 {
						t.Fatalf("invalid block created backups: %v, %v", entries, err)
					}
				})
			}
		})
	}
}

func TestWriteManagedBlockRequiresTemplateMarkers(t *testing.T) {
	root := t.TempDir()
	plan := NewPlan(slog.New(slog.DiscardHandler), root, t.TempDir())
	if err := plan.WriteManagedBlock(filepath.Join(root, "ignore"), "unmarked template\n", Sources); err == nil {
		t.Fatal("unmarked template accepted")
	}
}
