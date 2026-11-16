package journal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const gitTimeout = 5 * time.Second

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// currentRefs records the checked-out branch and commit of the project, keeping the
// supplied pull request and issue. When Git cannot report them, branch and commit stay
// unset and the failure is logged.
func (s *Service) currentRefs(ctx context.Context, refs Refs) Refs {
	refs.Branch, refs.Commit = "", ""
	branch, err := git(ctx, s.store.root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		s.logger.WarnContext(ctx, "Git branch unavailable", slog.Any("error", err))
		return refs
	}
	if branch != "HEAD" {
		refs.Branch = branch
	}

	commit, err := git(ctx, s.store.root, "rev-parse", "HEAD")
	if err != nil {
		s.logger.WarnContext(ctx, "Git commit unavailable", slog.Any("error", err))
		return refs
	}
	if commitPattern.MatchString(commit) {
		refs.Commit = commit
	}
	return refs
}

// git runs Git in root and returns its trimmed output. Errors include Git's own message.
func git(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// #nosec G204 -- Git and its arguments are fixed by the caller, without a shell.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()

	var exit *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSpace(string(output)), nil
	case ctx.Err() != nil:
		err = ctx.Err()
	case errors.As(err, &exit) && len(exit.Stderr) > 0:
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
	}
	return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}
