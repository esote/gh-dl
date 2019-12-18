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
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"time"
)

type repo struct {
	git  string
	name string
	user string
}

const sleep = 250 * time.Millisecond

var (
	base string

	level      int
	timeout    time.Duration
	submodules bool
)

func main() {
	name := fmt.Sprintf("gh-dl-%d.tar.gz", time.Now().UTC().Unix())

	log.SetFlags(0)
	log.SetPrefix("error: ")

	flag.IntVar(&level, "l", gzip.DefaultCompression,
		"gzip compression level")
	flag.DurationVar(&timeout, "t", 10*time.Minute,
		`git clone timeout duration, "0s" for none`)
	flag.BoolVar(&submodules, "s", false, "recursively fetch submodules")

	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatal("no names specified")
	}

	var err error
	base, err = ioutil.TempDir("", "gh-dl-")

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("using the working directory", base)

	repos := make(chan repo, 100)

	// Begin with 1 so that the concurrent downloads must wait for the
	// repository querying to finish.
	var wg sync.WaitGroup
	wg.Add(1)

	var total uint64
	var successful uint64

	go fanQueries(repos, &wg, &total, flag.Args())
	go fanDls(repos, &wg, &successful)

	wg.Wait()
	close(repos)

	if successful == 0 {
		err = errors.New("no repositories downloaded")
		goto done
	}

	fmt.Printf("downloaded %d/%d repositories\n", successful, total)
	fmt.Println("archiving...")

	if err = archive(name); err == nil {
		fmt.Println("archive created:", name)
	}

done:
	if err2 := os.RemoveAll(base); err2 != nil && err == nil {
		err = err2
	}

	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
