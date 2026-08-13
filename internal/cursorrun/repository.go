package cursorrun

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

// RepositoryInfo is the local-only repository preflight shown before a Cursor
// Cloud Agent run. Inspection never fetches or changes repository state.
type RepositoryInfo struct {
	Repository       bool   `json:"repository"`
	URL              string `json:"url,omitempty"`
	StartingRef      string `json:"starting_ref,omitempty"`
	Dirty            bool   `json:"dirty"`
	LocalOnlyCommits int    `json:"local_only_commits"`
	RemoteRefKnown   bool   `json:"remote_ref_known"`
	Warning          string `json:"warning,omitempty"`
}

var errInvalidGitHubRepository = errors.New(
	"cursor repository must be an HTTPS or git SSH URL for exactly one github.com owner/repository",
)

const unsupportedOriginWarning = "origin remote is not a supported credential-free GitHub repository"

// NormalizeGitHubRepository accepts the common GitHub HTTPS and git SSH remote
// forms and returns the credential-free HTTPS repository URL Cursor expects.
func NormalizeGitHubRepository(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", errInvalidGitHubRepository
	}

	const scpPrefix = "git@github.com:"
	if strings.HasPrefix(strings.ToLower(raw), scpPrefix) {
		path := raw[len(scpPrefix):]
		owner, repository, ok := splitGitHubRepositoryPath(path)
		if !ok {
			return "", errInvalidGitHubRepository
		}
		return "https://github.com/" + owner + "/" + repository, nil
	}

	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.RawPath != "" {
		return "", errInvalidGitHubRepository
	}
	if !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" {
		return "", errInvalidGitHubRepository
	}

	switch {
	case strings.EqualFold(parsed.Scheme, "https"):
		if parsed.User != nil {
			return "", errInvalidGitHubRepository
		}
	case strings.EqualFold(parsed.Scheme, "ssh"):
		if parsed.User == nil || parsed.User.Username() != "git" {
			return "", errInvalidGitHubRepository
		}
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return "", errInvalidGitHubRepository
		}
	default:
		return "", errInvalidGitHubRepository
	}

	path := strings.TrimPrefix(parsed.Path, "/")
	owner, repository, ok := splitGitHubRepositoryPath(path)
	if !ok {
		return "", errInvalidGitHubRepository
	}
	return "https://github.com/" + owner + "/" + repository, nil
}

func splitGitHubRepositoryPath(path string) (string, string, bool) {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") ||
		strings.ContainsAny(path, `\?#%`) {
		return "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner := parts[0]
	repository := strings.TrimSuffix(parts[1], ".git")
	if !validGitHubOwner(owner) || !validGitHubRepositoryName(repository) {
		return "", "", false
	}
	return owner, repository, true
}

func validGitHubOwner(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func validGitHubRepositoryName(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// InspectRepository reads repository metadata exclusively through non-mutating
// Git commands. GIT_OPTIONAL_LOCKS=0 prevents status inspection from refreshing
// the index on disk, and no command contacts a remote.
func InspectRepository(ctx context.Context, dir string) (RepositoryInfo, error) {
	var info RepositoryInfo
	if ctx == nil {
		ctx = context.Background()
	}

	inside, found, err := optionalGitOutput(ctx, dir,
		"rev-parse", "--is-inside-work-tree")
	if err != nil {
		return info, fmt.Errorf("inspect cursor repository: %w", err)
	}
	if !found || inside != "true" {
		return info, nil
	}
	info.Repository = true

	branch, onBranch, err := optionalGitOutput(ctx, dir,
		"symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return info, fmt.Errorf("inspect cursor repository ref: %w", err)
	}
	if onBranch && branch != "" {
		info.StartingRef = branch
	} else {
		info.StartingRef, err = requiredGitOutput(ctx, dir,
			"rev-parse", "--verify", "HEAD")
		if err != nil {
			return info, fmt.Errorf("inspect cursor repository detached ref: %w", err)
		}
	}

	status, err := requiredGitOutput(ctx, dir,
		"status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return info, fmt.Errorf("inspect cursor repository status: %w", err)
	}
	info.Dirty = status != ""

	origin, hasOrigin, err := optionalGitOutput(ctx, dir,
		"config", "--get", "remote.origin.url")
	if err != nil {
		return info, fmt.Errorf("inspect cursor repository origin: %w", err)
	}
	originPresent := hasOrigin && origin != ""
	unsupportedOrigin := false
	if originPresent {
		normalized, normalizeErr := NormalizeGitHubRepository(origin)
		if normalizeErr != nil {
			unsupportedOrigin = true
		} else {
			info.URL = normalized
		}
	}

	if originPresent {
		if onBranch && branch != "" {
			info.RemoteRefKnown, info.LocalOnlyCommits, err =
				inspectBranchRemoteState(ctx, dir, branch)
		} else {
			info.RemoteRefKnown, info.LocalOnlyCommits, err =
				inspectDetachedRemoteState(ctx, dir)
		}
		if err != nil {
			return info, err
		}
	}

	var warnings []string
	switch {
	case !originPresent:
		warnings = append(warnings, "origin remote is not configured")
	case unsupportedOrigin:
		warnings = append(warnings, unsupportedOriginWarning)
	}
	if originPresent && !info.RemoteRefKnown {
		warnings = append(warnings, "origin remote-tracking ref is not available locally")
	}
	if info.Dirty {
		warnings = append(warnings, "uncommitted changes are not available to Cursor")
	}
	if info.LocalOnlyCommits > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d local commit(s) are not available to Cursor", info.LocalOnlyCommits,
		))
	}
	info.Warning = strings.Join(warnings, "; ")
	return info, nil
}

func inspectBranchRemoteState(
	ctx context.Context,
	dir string,
	branch string,
) (bool, int, error) {
	upstream, hasUpstream, err := optionalGitOutput(ctx, dir,
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return false, 0, fmt.Errorf("inspect cursor repository upstream: %w", err)
	}

	remoteRef := "refs/remotes/origin/" + branch
	if hasUpstream && strings.HasPrefix(upstream, "origin/") {
		remoteRef = "refs/remotes/" + upstream
	}
	_, known, err := optionalGitOutput(ctx, dir,
		"rev-parse", "--verify", "--quiet", remoteRef+"^{commit}")
	if err != nil {
		return false, 0, fmt.Errorf("inspect cursor repository remote ref: %w", err)
	}
	if !known {
		return false, 0, nil
	}
	count, err := countLocalOnlyCommits(ctx, dir, remoteRef+"..HEAD")
	return true, count, err
}

func inspectDetachedRemoteState(
	ctx context.Context,
	dir string,
) (bool, int, error) {
	rawRefs, err := requiredGitOutput(ctx, dir,
		"for-each-ref", "--format=%(refname)", "refs/remotes/origin")
	if err != nil {
		return false, 0, fmt.Errorf("inspect cursor repository remote refs: %w", err)
	}
	refs := strings.Fields(rawRefs)
	if len(refs) == 0 {
		return false, 0, nil
	}
	args := []string{"rev-list", "--count", "HEAD", "--not"}
	args = append(args, refs...)
	rawCount, err := requiredGitOutput(ctx, dir, args...)
	if err != nil {
		return false, 0, fmt.Errorf("inspect cursor repository local commits: %w", err)
	}
	count, err := parseCommitCount(rawCount)
	if err != nil {
		return false, 0, err
	}
	return true, count, nil
}

func countLocalOnlyCommits(
	ctx context.Context,
	dir string,
	revisionRange string,
) (int, error) {
	rawCount, err := requiredGitOutput(ctx, dir,
		"rev-list", "--count", revisionRange)
	if err != nil {
		return 0, fmt.Errorf("inspect cursor repository local commits: %w", err)
	}
	return parseCommitCount(rawCount)
}

func parseCommitCount(raw string) (int, error) {
	count, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || count < 0 {
		return 0, errors.New("inspect cursor repository: invalid local commit count")
	}
	return count, nil
}

func requiredGitOutput(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	out, found, err := optionalGitOutput(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("git command failed")
	}
	return out, nil
}

func optionalGitOutput(
	ctx context.Context,
	dir string,
	args ...string,
) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), true, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", false, ctxErr
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return "", false, nil
	}
	return "", false, err
}
