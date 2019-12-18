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
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"time"
)

const (
	sleep = 250 * time.Millisecond
)

var (
	// Base working directory.
	base string

	// Flags
	level      int
	quiet      bool
	submodules bool
	timeout    time.Duration
	verbose    bool

	// Stat counters
	successful uint64
	total      uint64
)

type msg struct {
	s string
	v bool
}

var msgs chan msg
var errs chan error

func main() {
	start := time.Now()

	log.SetFlags(0)
	log.SetPrefix("error: ")

	flag.IntVar(&level, "l", gzip.DefaultCompression,
		"gzip compression level")
	flag.BoolVar(&quiet, "q", false, "quiet except for fatal errors")
	flag.BoolVar(&submodules, "s", false, "recursively fetch submodules")
	flag.DurationVar(&timeout, "t", 10*time.Minute,
		`git clone timeout duration, "0s" for none`)
	flag.BoolVar(&verbose, "v", false, "print more details")

	flag.Parse()

	if quiet && verbose {
		log.Fatal("quiet and verbose flags are mutually exclusive")
	}

	if flag.NArg() == 0 {
		log.Fatal("no names specified")
	}

	var err error
	base, err = ioutil.TempDir("", "gh-dl-")

	if err != nil {
		log.Fatal(err)
	}

	if verbose {
		fmt.Println("working directory", base)
	}

	errs = make(chan error)
	go func(errs <-chan error) {
		for err := range errs {
			if !quiet {
				log.Println(err)
			}
		}
	}(errs)

	msgs = make(chan msg)
	go func(msgs <-chan msg) {
		for m := range msgs {
			if !quiet && (!m.v || (m.v && verbose)) {
				fmt.Println(m.s)
			}
		}
	}(msgs)

	queries := make(chan query, flag.NArg())
	dls := make(chan dl, 100)

	var wg sync.WaitGroup
	wg.Add(flag.NArg())

	go consumeQueries(queries, dls, &wg)
	go fanQueries(flag.Args(), queries, &wg)
	go consumeDls(dls, &wg)

	wg.Wait()
	close(queries)
	close(dls)
	close(errs)
	close(msgs)

	if !quiet {
		fmt.Printf("downloaded %d/%d repositories\n", successful, total)
	}

	if verbose {
		fmt.Println("archiving...")
	}

	name := fmt.Sprintf("gh-dl-%d.tar.gz", start.UTC().Unix())

	if err = archive(name); err == nil && !quiet {
		fmt.Println("archive created:", name)
	}

	if err2 := os.RemoveAll(base); err2 != nil && err == nil {
		err = err2
	}

	if err != nil {
		log.Fatal(err)
	}

}
