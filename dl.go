/*
 * gh-dl is a GitHub archiving client.
 * Copyright (C) 2019 Esote
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published
 * by the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type dl struct {
	git   string
	name  string
	owner string
}

func consumeDls(in <-chan dl, wg *sync.WaitGroup) {
	for dl := range in {
		if excluded[dl.name] {
			msgs <- msg{
				s: fmt.Sprintf("skipped %s", dl.name),
				v: true,
			}
			wg.Done()
			continue
		}

		go download(dl, wg)
		time.Sleep(sleep)
	}
}

func download(in dl, wg *sync.WaitGroup) {
	defer wg.Done()
	ctx := context.Background()

	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"-C", filepath.Join(base, in.owner), "clone", "-q",
		"--no-hardlinks"}

	if submodules {
		args = append(args, "--recurse-submodules", "-j", "16")
	}

	args = append(args, in.git)

	cmd := exec.CommandContext(ctx, "git", args...)

	if _, err := cmd.Output(); err != nil {
		_ = os.RemoveAll(filepath.Join(base, in.name))
		if ctx.Err() == context.DeadlineExceeded {
			err = ctx.Err()
		}
		msgs <- errors.New(in.name + ": " + err.Error())
		return
	}

	msgs <- msg{
		s: fmt.Sprintf("downloaded repo %s", in.name),
		v: true,
	}

	atomic.AddUint64(&successful, 1)
}
