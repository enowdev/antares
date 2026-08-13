package cursorrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeGitHubRepository(t *testing.T) {
	tests := map[string]string{
		"git@github.com:owner/repo.git":       "https://github.com/owner/repo",
		"ssh://git@github.com/owner/repo.git": "https://github.com/owner/repo",
		"https://github.com/owner/repo.git":   "https://github.com/owner/repo",
		"https://github.com/owner/repo":       "https://github.com/owner/repo",
		"https://GITHUB.COM/Owner/Repo.git":   "https://github.com/Owner/Repo",
	}
	for in, want := range tests {
		got, err := NormalizeGitHubRepository(in)
		if err != nil || got != want {
			t.Errorf("%q => %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeGitHubRepositoryRejectsUnsafeRemotes(t *testing.T) {
	tests := []string{
		"",
		"/tmp/repo",
		"./repo",
		"file:///tmp/repo",
		"github.com/owner/repo",
		"http://github.com/owner/repo",
		"https://gitlab.com/owner/repo",
		"git@gitlab.com:owner/repo.git",
		"https://github.com/owner",
		"https://github.com/owner/repo/extra",
		"https://github.com/owner/repo/",
		"https://github.com/owner%2Frepo",
		"https://github.com/owner/../repo",
		"https://user:secret@github.com/owner/repo",
		"ssh://token@github.com/owner/repo.git",
		"ssh://git:secret@github.com/owner/repo.git",
		"ssh://git@github.com:22/owner/repo.git",
		"https://github.com/owner/repo?token=secret",
		"https://github.com/owner/repo#secret",
		"git@github.com:owner/repo.git?token=secret",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			if got, err := NormalizeGitHubRepository(in); err == nil {
				t.Fatalf("%q => %q, want rejection", in, got)
			}
		})
	}
}

func TestInspectRepositoryReportsLinkedWorktreeStateWithoutMutation(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	main := filepath.Join(root, "main")
	linked := filepath.Join(root, "linked")

	runCommand(t, root, "git", "init", "--bare", "--initial-branch=main", bare)
	runCommand(t, root, "git", "init", "--initial-branch=main", main)
	configureTestGit(t, main)
	writeTestFile(t, filepath.Join(main, "tracked.txt"), "main\n")
	runGit(t, main, "add", "tracked.txt")
	runGit(t, main, "commit", "-m", "main")
	runGit(t, main, "remote", "add", "origin", bare)
	runGit(t, main, "push", "-u", "origin", "main")

	runGit(t, main, "worktree", "add", "-b", "feature", linked)
	writeTestFile(t, filepath.Join(linked, "tracked.txt"), "feature\n")
	runGit(t, linked, "add", "tracked.txt")
	runGit(t, linked, "commit", "-m", "remote feature")
	runGit(t, linked, "push", "-u", "origin", "feature")
	runGit(t, main, "remote", "set-url", "origin", "git@github.com:owner/repo.git")

	writeTestFile(t, filepath.Join(linked, "tracked.txt"), "local-only\n")
	runGit(t, linked, "add", "tracked.txt")
	runGit(t, linked, "commit", "-m", "local only")
	writeTestFile(t, filepath.Join(linked, "dirty.txt"), "not committed\n")

	gitFile, err := os.Stat(filepath.Join(linked, ".git"))
	if err != nil || !gitFile.Mode().IsRegular() {
		t.Fatalf("linked worktree .git is not a file: info=%v err=%v", gitFile, err)
	}
	before := repositorySnapshot(t, linked)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := InspectRepository(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Repository {
		t.Fatal("linked worktree was not detected as a repository")
	}
	if info.URL != "https://github.com/owner/repo" {
		t.Fatalf("url=%q", info.URL)
	}
	if info.StartingRef != "feature" {
		t.Fatalf("starting_ref=%q, want feature", info.StartingRef)
	}
	if !info.Dirty {
		t.Fatal("dirty=false, want true")
	}
	if !info.RemoteRefKnown {
		t.Fatal("remote_ref_known=false despite refs/remotes/origin/feature")
	}
	if info.LocalOnlyCommits != 1 {
		t.Fatalf("local_only_commits=%d, want 1", info.LocalOnlyCommits)
	}
	if info.Warning == "" {
		t.Fatal("warning is empty for dirty, locally-ahead worktree")
	}
	if after := repositorySnapshot(t, linked); !reflect.DeepEqual(after, before) {
		t.Fatalf("inspection mutated repository state:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestInspectRepositorySupportsDetachedHEAD(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")

	runCommand(t, root, "git", "init", "--bare", "--initial-branch=main", bare)
	runCommand(t, root, "git", "init", "--initial-branch=main", repo)
	configureTestGit(t, repo)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "main\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "main")
	runGit(t, repo, "remote", "add", "origin", bare)
	runGit(t, repo, "push", "-u", "origin", "main")
	wantSHA := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "switch", "--detach", wantSHA)
	runGit(t, repo, "remote", "set-url", "origin", "ssh://git@github.com/owner/repo.git")

	info, err := InspectRepository(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Repository || info.URL != "https://github.com/owner/repo" {
		t.Fatalf("repository identity = %+v", info)
	}
	if info.StartingRef != wantSHA {
		t.Fatalf("starting_ref=%q, want detached SHA %q", info.StartingRef, wantSHA)
	}
	if info.Dirty || info.LocalOnlyCommits != 0 || !info.RemoteRefKnown {
		t.Fatalf("unexpected detached state: %+v", info)
	}
}

func TestInspectRepositoryReturnsNonRepositoryWithoutError(t *testing.T) {
	requireGit(t)
	info, err := InspectRepository(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Repository {
		t.Fatalf("plain directory reported as repository: %+v", info)
	}
}

func TestInspectRepositoryRejectsUnsafeOrigin(t *testing.T) {
	requireGit(t)
	tests := []string{
		"https://gitlab.com/owner/repo.git",
		"https://user:secret@github.com/owner/repo.git",
	}
	for _, remote := range tests {
		t.Run(remote, func(t *testing.T) {
			repo := initTestRepository(t)
			runGit(t, repo, "remote", "add", "origin", remote)
			if info, err := InspectRepository(context.Background(), repo); err == nil {
				t.Fatalf("unsafe origin accepted: %+v", info)
			}
		})
	}
}

func TestInspectRepositoryWarnsWhenOriginTrackingRefIsUnknown(t *testing.T) {
	requireGit(t)
	repo := initTestRepository(t)
	runGit(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")

	info, err := InspectRepository(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Repository || info.StartingRef != "main" {
		t.Fatalf("unexpected repository state: %+v", info)
	}
	if info.RemoteRefKnown || info.LocalOnlyCommits != 0 || info.Warning == "" {
		t.Fatalf("unknown remote ref not reported safely: %+v", info)
	}
}

type repoSnapshot struct {
	Head   string
	Status string
	Refs   string
	Config string
}

func repositorySnapshot(t *testing.T, dir string) repoSnapshot {
	t.Helper()
	return repoSnapshot{
		Head:   runGit(t, dir, "rev-parse", "HEAD"),
		Status: runGit(t, dir, "status", "--porcelain=v1", "--untracked-files=all"),
		Refs:   runGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)"),
		Config: runGit(t, dir, "config", "--local", "--list"),
	}
}

func initTestRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	runCommand(t, filepath.Dir(repo), "git", "init", "--initial-branch=main", repo)
	configureTestGit(t, repo)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "initial\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func configureTestGit(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.name", "Task 8 Test")
	runGit(t, dir, "config", "user.email", "task8@example.invalid")
	runGit(t, dir, "config", "commit.gpgsign", "false")
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runCommand(t, dir, "git", append([]string{"-C", dir}, args...)...)
}

func runCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
