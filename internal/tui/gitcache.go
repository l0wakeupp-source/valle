package tui

import (
	"sync"
	"time"

	"rick/internal/session"
)

// Git state is read by the status bar, which renders on every frame — during
// streaming that is 25 times a second. Each read spawns two git subprocesses
// and `git status --porcelain` walks the entire worktree, which cost ~25ms per
// frame on Windows and made the whole UI feel sluggish.
//
// The branch changes rarely and nothing here is load-bearing, so it is cached
// and refreshed off the render path.

const gitTTL = 5 * time.Second

var gitCache struct {
	sync.Mutex
	dir     string
	branch  string
	at      time.Time
	loading bool
}

// cachedGitBranch returns the branch for dir without ever blocking the
// renderer. A cold or stale entry triggers a background refresh and returns
// whatever is known now (possibly "").
func cachedGitBranch(dir string) string {
	gitCache.Lock()
	defer gitCache.Unlock()

	if gitCache.dir != dir {
		// Directory changed: drop the old value rather than show a stale one.
		gitCache.dir, gitCache.branch, gitCache.at = dir, "", time.Time{}
	}
	if !gitCache.loading && time.Since(gitCache.at) > gitTTL {
		gitCache.loading = true
		go refreshGitBranch(dir)
	}
	return gitCache.branch
}

// refreshGitBranch does the expensive work on its own goroutine.
func refreshGitBranch(dir string) {
	branch := parseGitBranch(session.GitInfo(dir))

	gitCache.Lock()
	defer gitCache.Unlock()
	gitCache.loading = false
	if gitCache.dir == dir { // ignore a result for a directory we left
		gitCache.branch, gitCache.at = branch, time.Now()
	}
}

// CachedGitBranch exposes the cache for tests.
func CachedGitBranch(dir string) string { return cachedGitBranch(dir) }
