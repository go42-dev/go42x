package agentenv

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

// gitIgnoreEscaper quotes the characters that gitignore patterns treat as wildcards.
var gitIgnoreEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)

// updateGitIgnore refreshes the managed .gitignore blocks. buildDir is the configured
// build directory; when it is empty, the project block, which needs it, stays unchanged.
func updateGitIgnore(root, buildDir string) error {
	plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), root, filepath.Join(root, config.Directory, "backups"))
	var err error
	if len(buildDir) == 0 {
		err = prepareDirGitIgnore(plan, root)
	} else {
		err = prepareGitIgnore(plan, root, buildDir)
	}
	if err != nil {
		return err
	}

	return plan.Apply()
}

// prepareGitIgnore refreshes the managed blocks of the project .gitignore and of
// .go42x/.gitignore. buildDir is the configured build directory; a relative path uses
// root.
func prepareGitIgnore(plan *fspatch.Plan, root, buildDir string) error {
	block, err := projectGitIgnore(root, buildDir)
	if err != nil {
		return err
	}

	err = plan.WriteManagedBlock(filepath.Join(root, ".gitignore"), block, fspatch.KindSources)
	if err != nil {
		return err
	}

	return prepareDirGitIgnore(plan, root)
}

// prepareDirGitIgnore refreshes the managed block of .go42x/.gitignore, which ignores
// the state directories inside .go42x.
func prepareDirGitIgnore(plan *fspatch.Plan, root string) error {
	return plan.WriteManagedBlock(filepath.Join(root, config.Directory, ".gitignore"),
		assets.AgentEnvDirGitIgnore(), fspatch.KindSources)
}

// projectGitIgnore renders the managed block of the project .gitignore. It ignores the
// build directory only when the directory lies inside the project.
func projectGitIgnore(root, buildDir string) (string, error) {
	tmpl, err := template.New(".gitignore").Option("missingkey=error").Parse(assets.AgentEnvGitIgnore())
	if err != nil {
		return "", fmt.Errorf("parse .gitignore template: %w", err)
	}

	var block strings.Builder
	err = tmpl.Execute(&block, struct{ BuildDir string }{BuildDir: gitIgnoreBuildDir(root, buildDir)})
	if err != nil {
		return "", fmt.Errorf("render .gitignore template: %w", err)
	}

	return block.String(), nil
}

// gitIgnoreBuildDir returns the escaped root-relative path of a build directory inside
// the project, or an empty string for the project root, a directory outside it, or a
// path that a gitignore line cannot hold.
func gitIgnoreBuildDir(root, buildDir string) string {
	root, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(buildDir) {
		buildDir = filepath.Join(root, buildDir)
	}

	rel, err := filepath.Rel(root, buildDir)
	if err != nil || rel == "." || !filepath.IsLocal(rel) || strings.ContainsAny(rel, "\r\n") {
		return ""
	}

	return gitIgnoreEscaper.Replace(filepath.ToSlash(rel))
}
