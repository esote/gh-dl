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
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"golang.org/x/term"
)

type msg struct {
	s string
	v bool
}

const (
	defaultTimeout = 10 * time.Minute
	dlBacklog      = 100
	sleep          = time.Second
	workers        = 10
)

var (
	// Flags
	auth       bool
	level      int
	quiet      bool
	submodules bool
	timeout    time.Duration
	verbose    bool
	exclude    string

	// Authentication token
	password string

	// Excluded repos
	excluded map[string]bool

	// Stat counters
	successful uint64
	total      uint64

	// Output stream
	msgs chan interface{}
)

func main() {
	name := fmt.Sprintf("gh-dl-%d.tar.gz", time.Now().UTC().Unix())

	log.SetFlags(0)
	log.SetPrefix("error: ")

	flag.BoolVar(&auth, "a", false,
		`enter personal authentication token (uses ssh for cloning private repos)`)
	flag.IntVar(&level, "l", gzip.DefaultCompression, "gzip compression level")
	flag.BoolVar(&quiet, "q", false, "quiet except for fatal errors")
	flag.BoolVar(&submodules, "s", false, "recursively fetch submodules")
	flag.DurationVar(&timeout, "t", defaultTimeout,
		`git clone timeout duration, "0s" for none`)
	flag.BoolVar(&verbose, "v", false, "print more details")
	flag.StringVar(&exclude, "x", "", "exclude comma-separated list of repos")
	flag.Parse()

	if quiet && verbose {
		log.Fatal("quiet and verbose flags are mutually exclusive")
	}

	if flag.NArg() == 0 {
		log.Fatal("no names specified")
	}

	base, err := ioutil.TempDir("", "gh-dl-")
	if err != nil {
		log.Fatal(err)
	}

	if verbose {
		fmt.Println("working directory", base)
	}

	excluded = make(map[string]bool)
	ex := strings.Split(exclude, ",")
	for _, x := range ex {
		excluded[x] = true
	}

	msgs = make(chan interface{})
	defer close(msgs)

	go func() {
		for m := range msgs {
			if quiet {
				continue
			}

			switch m := m.(type) {
			case msg:
				if !m.v || verbose {
					fmt.Println(m.s)
				}
			case error:
				log.Println(m)
			}
		}
	}()

	var client *github.Client
	if auth {
		fmt.Print("Personal access token: ")
		bytepass, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			log.Fatal(err)
		}
		password = string(bytepass)
		fmt.Println()
		ctx := context.Background()
		oauth := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: password,
		}))
		client = github.NewClient(oauth)
	} else {
		client = github.NewClient(nil)
	}
	client.UserAgent = "gh-dl"

	queries := make(chan query, flag.NArg())
	dls := make(chan dl, dlBacklog)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		go consumeQueries(client, base, queries, dls, &wg)
		go consumeDls(base, dls, &wg)
	}

	wg.Add(flag.NArg())
	for _, arg := range flag.Args() {
		split := strings.Split(arg, "/")
		switch len(split) {
		case 1:
			queries <- query{
				kind:  queryUser,
				owner: arg,
			}
		case 2:
			queries <- query{
				kind:  queryRepo,
				owner: split[0],
				repo:  split[1],
			}
		default:
			msgs <- fmt.Errorf("arg %s invalid", arg)
			wg.Done()
		}
	}

	wg.Wait()
	close(queries)
	close(dls)

	msgs <- msg{
		s: fmt.Sprintf("downloaded %d/%d repos", successful, total),
		v: false,
	}

	if successful == 0 {
		err = errors.New("failed to download any repos")
		goto out
	}

	msgs <- msg{
		s: "archiving...",
		v: true,
	}

	if err = archive(base, name); err == nil {
		msgs <- msg{
			s: fmt.Sprintf("archive created: %s", name),
			v: false,
		}
	}

out:
	if err2 := os.RemoveAll(base); err2 != nil && err == nil {
		err = err2
	}

	if err != nil {
		log.Fatal(err)
	}
}
