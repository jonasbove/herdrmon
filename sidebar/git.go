package main

// Git events per working directory: a commit (HEAD moved) or a push (a
// remote-tracking ref moved), since the last look. Polled at most every 2 s
// per cwd, never on the frame path.

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

const gitMS = 2000

type gitSeen struct {
	looked        bool    // false until the first full look
	head, remotes *string // nil = not a repo / failed
	checked       int64
	pending       bool
}

var (
	gitMu     sync.Mutex
	gitSeenAt = map[string]*gitSeen{}
)

func gitRun(cwd string, args ...string) *string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
	if err != nil {
		return nil
	}
	s := jsTrim(string(out))
	return &s
}

type GitEvents struct{ Commit, Push bool }

func sameRef(a, b *string) bool { return *a == *b }

// gitEvents: what happened in cwd's repository since the last look. It
// blocks for the git calls; the daemon runs it off the frame path.
func gitEvents(cwd string, now int64) GitEvents {
	gitMu.Lock()
	g := gitSeenAt[cwd]
	if g != nil && (g.pending || now-g.checked < gitMS) {
		gitMu.Unlock()
		return GitEvents{}
	}
	var prev gitSeen
	if g != nil {
		prev = *g
	}
	next := prev
	next.pending = true
	gitSeenAt[cwd] = &next
	gitMu.Unlock()

	top := gitRun(cwd, "rev-parse", "--show-toplevel")
	if top == nil || *top == "" {
		gitMu.Lock()
		gitSeenAt[cwd] = &gitSeen{looked: true, checked: now}
		gitMu.Unlock()
		return GitEvents{}
	}
	var head, remotes *string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); head = gitRun(*top, "rev-parse", "HEAD") }()
	go func() {
		defer wg.Done()
		remotes = gitRun(*top, "for-each-ref", "--format=%(refname) %(objectname)", "refs/remotes")
	}()
	wg.Wait()
	gitMu.Lock()
	gitSeenAt[cwd] = &gitSeen{looked: true, head: head, remotes: remotes, checked: now}
	gitMu.Unlock()
	if g == nil || !prev.looked {
		return GitEvents{}
	}
	return GitEvents{
		Commit: prev.head != nil && head != nil && !sameRef(head, prev.head),
		Push:   prev.remotes != nil && remotes != nil && !sameRef(remotes, prev.remotes),
	}
}
