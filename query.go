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
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	queryRepo = iota
	queryUser
)

type query struct {
	kind  int
	name  string
	owner string
}

type repo struct {
	Name   string `json:"full_name"`
	GitURL string `json:"git_url"`
}

func fanQueries(args []string, out chan<- query, wg *sync.WaitGroup) {
	for _, arg := range args {
		split := strings.Split(arg, "/")
		switch len(split) {
		case 1:
			out <- query{
				kind:  queryUser,
				owner: arg,
			}
		case 2:
			out <- query{
				kind:  queryRepo,
				name:  arg,
				owner: split[0],
			}
		default:
			errs <- fmt.Errorf("arg %s invalid", arg)
			wg.Done()
		}
	}
}

func consumeQueries(in <-chan query, out chan<- dl, wg *sync.WaitGroup) {
	for q := range in {
		go func(q query) {
			if err := mkdir(q.owner); err != nil {
				errs <- err
				wg.Done()
				return
			}

			switch q.kind {
			case queryRepo:
				msgs <- msg{
					s: fmt.Sprintf("added individual repo %s", q.name),
					v: true,
				}
				git := &url.URL{
					Scheme: "git",
					Host:   "github.com",
					Path:   q.name + ".git",
				}

				atomic.AddUint64(&total, 1)
				out <- dl{
					git:   git.String(),
					name:  q.name,
					owner: q.owner,
				}
			case queryUser:
				go discoverRepos(q, out, wg)
			}
		}(q)
		time.Sleep(sleep)
	}
}

func discoverRepos(in query, out chan<- dl, wg *sync.WaitGroup) {
	defer wg.Done()
	api, err := url.Parse("https://api.github.com")

	if err != nil {
		errs <- err
		return
	}

	api.Path = filepath.Join("users", in.owner, "repos")

	v := url.Values{}
	v.Add("page_size", "100")

	var count int

	for page, pages := 1, 1; page <= pages; page++ {
		v.Set("page", strconv.Itoa(page))
		api.RawQuery = v.Encode()

		repos, err := requestRepos(api.String(), &pages)

		if err != nil {
			errs <- err
			return
		}

		if len(repos) == 0 {
			break
		}

		count += len(repos)
		wg.Add(len(repos))

		for _, r := range repos {
			out <- dl{
				git:   r.GitURL,
				name:  r.Name,
				owner: in.owner,
			}
		}

		time.Sleep(sleep)
	}

	msgs <- msg{
		s: fmt.Sprintf("found %d repos for %s", count, in.owner),
		v: false,
	}
	atomic.AddUint64(&total, uint64(count))
}

func requestRepos(url string, pages *int) (repos []repo, err error) {
	resp, err := http.Get(url)

	if err != nil {
		return
	}

	if err = checkRateLimit(resp.Header); err != nil {
		return
	}

	body, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		return
	}

	defer resp.Body.Close()

	if err = json.Unmarshal(body, &repos); err != nil {
		return
	}

	if n := checkPageCount(resp.Header.Get("Link")); n != -1 {
		*pages = n
	}

	return
}

func checkRateLimit(hdrs http.Header) error {
	if rem := hdrs.Get("X-RateLimit-Remaining"); rem != "0" {
		msgs <- msg{
			s: fmt.Sprintf("rate limit remaining: %s", rem),
			v: true,
		}
		return nil
	}

	reset, err := strconv.ParseInt(hdrs.Get("X-RateLimit-Remaining"), 10, 64)

	if err != nil {
		return errors.New("rate limit exceeded")
	}

	return fmt.Errorf("rate limit exceeded, will reset %s",
		time.Unix(reset, 0).String())
}

func checkPageCount(link string) int {
	links := strings.Split(link, ", ")

	if len(links) == 0 {
		return -1
	}

	// "last" reference is usually at the end so traverse in reverse
	for i := len(links) - 1; i > 0; i-- {
		parts := strings.Split(links[i], "; ")

		if len(parts) != 2 || len(parts[0]) < 2 || parts[1] != `rel="last"` {
			continue
		}

		u, err := url.Parse(parts[0][1 : len(parts[0])-2])

		if err != nil {
			return -1
		}

		n, err := strconv.Atoi(u.Query().Get("page"))

		if err != nil {
			return -1
		}

		return n
	}

	return -1
}

func mkdir(name string) error {
	err := os.Mkdir(filepath.Join(base, name), 0700)

	if err == nil || os.IsExist(err) {
		return nil
	}

	return err
}
