package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

func fanDls(repos <-chan repo, wg *sync.WaitGroup, successful *uint64) {
	for r := range repos {
		go func(r repo) {
			defer wg.Done()
			if err := dl(r); err == nil {
				atomic.AddUint64(successful, 1)
			} else {
				log.Println(r.name, err)
			}
		}(r)
		time.Sleep(sleep)
	}
}

func dl(r repo) error {
	ctx := context.Background()

	if *timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	args := []string{"-C", filepath.Join(base, r.user), "clone", "-q",
		"--no-hardlinks"}

	if *submodules {
		args = append(args, "--recurse-submodules", "-j", "16")
	}

	args = append(args, r.git)

	cmd := exec.CommandContext(ctx, "git", args...)

	if _, err := cmd.Output(); err != nil {
		_ = os.RemoveAll(filepath.Join(base, r.name))
		if ctx.Err() == context.DeadlineExceeded {
			return ctx.Err()
		}
		return err
	}

	return nil
}
