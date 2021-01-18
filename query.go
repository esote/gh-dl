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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-github/github"
)

const (
	queryRepo = iota
	queryUser
)

type query struct {
	kind  int
	owner string
	repo  string
}

func consumeQueries(client *github.Client, base string, in <-chan query, out chan<- dl, wg *sync.WaitGroup) {
	for query := range in {
		go queryOwner(client, base, query, out, wg)
		time.Sleep(sleep)
	}
}

func queryOwner(client *github.Client, base string, in query, out chan<- dl, wg *sync.WaitGroup) {
	if err := mkdir(base, in.owner); err != nil {
		msgs <- err
		wg.Done()
		return
	}

	switch in.kind {
	case queryRepo:
		repo, _, err := client.Repositories.Get(context.Background(), in.owner, in.repo)
		if err != nil {
			msgs <- err
			return
		}
		out <- dl{
			git:      *repo.GitURL,
			ssh:      *repo.SSHURL,
			fullname: *repo.FullName,
			owner:    in.owner,
			private:  *repo.Private,
		}

		msgs <- msg{
			s: fmt.Sprintf("added individual repo %s", *repo.FullName),
			v: true,
		}
		atomic.AddUint64(&total, 1)
	case queryUser:
		go discoverRepos(client, in, out, wg)
	}
}

func discoverRepos(client *github.Client, in query, out chan<- dl, wg *sync.WaitGroup) {
	defer wg.Done()

	ctx := context.Background()
	opt := &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	query := fmt.Sprintf(`user:"%s"`, in.owner)
	var count uint64
	for {
		result, resp, err := client.Search.Repositories(ctx, query, opt)
		if err != nil {
			log.Fatal(err)
		}
		count += uint64(len(result.Repositories))
		wg.Add(len(result.Repositories))
		for _, r := range result.Repositories {
			out <- dl{
				git:      *r.GitURL,
				ssh:      *r.SSHURL,
				fullname: *r.FullName,
				owner:    in.owner,
				private:  *r.Private,
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
		time.Sleep(sleep)
	}

	msgs <- msg{
		s: fmt.Sprintf("found %d repos for %s", count, in.owner),
		v: false,
	}
	atomic.AddUint64(&total, count)
}

func mkdir(base, name string) error {
	err := os.Mkdir(filepath.Join(base, name), 0700)
	if err == nil || os.IsExist(err) {
		return nil
	}
	return err
}
