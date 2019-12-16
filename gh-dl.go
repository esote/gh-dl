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
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type repo struct {
	git  string
	name string
	user string
}

var base string

var mu sync.Mutex

func usage() {
	fmt.Println(`Usage of gh-dl:
  -l int
        zip compression level (-1 = default, 0 = none, 9 = best)
  -t duration
        git clone timeout duration, or 0 for none (default 10m0s)`)
}

func main() {
	name := fmt.Sprintf("gh-dl-%d.tar.gz", time.Now().UTC().Unix())

	log.SetFlags(0)
	log.SetPrefix("fail: ")

	level := flag.Int("l", gzip.DefaultCompression,
		"gzip compression level, default = 1, 0 (none) <= level <= 9 (best)")
	timeout := flag.Duration("t", 10*time.Minute,
		"git clone timeout duration, none = 0")

	flag.Usage = usage
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatal("no username specified")
	}

	if *level != gzip.DefaultCompression && (*level < gzip.NoCompression || *level > gzip.BestCompression) {
		log.Fatal("gzip compression level invalid")
	}

	var err error

	base, err = ioutil.TempDir("", "gh-dl-")

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("using the working directory", base)

	repos := make(chan repo)

	var dlWg sync.WaitGroup

	// Begin with 1 so that the concurrent downloads must wait for the
	// repository querying to finish.
	dlWg.Add(1)

	go func() {
		var queryWg sync.WaitGroup

		queryWg.Add(flag.NArg())

		for _, user := range flag.Args() {
			go query(user, repos, &dlWg, &queryWg)
		}

		queryWg.Wait()
		dlWg.Done()
	}()

	successful := 0

	go func() {
		for r := range repos {
			go dl(r, *timeout, &dlWg, &successful)
			time.Sleep(200 * time.Millisecond)
		}
	}()

	dlWg.Wait()
	close(repos)

	if successful == 0 {
		err = errors.New("no repositories downloaded")
		goto done
	} else {
		fmt.Printf("downloaded %d repositories\n", successful)
	}

	_, _ = fmt.Println("archiving...")

	if err = archive(name, *level); err != nil {
		goto done
	}

	fmt.Println("archive created:", name)

done:
	if err2 := os.RemoveAll(base); err2 != nil {
		if err == nil {
			err = err2
		}
	}

	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func query(user string, repos chan<- repo, dlWg, queryWg *sync.WaitGroup) {
	defer queryWg.Done()

	if err := os.Mkdir(filepath.Join(base, user), 0700); err != nil {
		log.Println(err)
		return
	}

	url := "https://api.github.com/users/" + user + "/repos"

	resp, err := http.Get(url)

	if err != nil {
		log.Println(err)
		return
	}

	data, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		log.Println(err)
		return
	}

	if err = resp.Body.Close(); err != nil {
		log.Println(err)
		return
	}

	var st []struct {
		Name   string `json:"full_name"`
		GitURL string `json:"git_url"`
	}

	if err := json.Unmarshal(data, &st); err != nil {
		log.Println(err)
		return
	}

	mu.Lock()
	_, _ = fmt.Printf("found %d repositories for %s\n", len(st), user)
	mu.Unlock()

	dlWg.Add(len(st))

	for _, r := range st {
		repos <- repo{
			git:  r.GitURL,
			name: r.Name,
			user: user,
		}
	}
}

func dl(r repo, timeout time.Duration, dlWg *sync.WaitGroup, successful *int) {
	defer dlWg.Done()

	ctx := context.Background()

	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	dir := filepath.Join(base, r.user)
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "clone", "-q", r.git)

	if _, err := cmd.Output(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Println(r.name, "(clone timeout)")
		} else {
			log.Println(r.name, err)
		}
		_ = os.RemoveAll(filepath.Join(base, r.name))
		return
	}

	mu.Lock()
	fmt.Println("done:", r.name)
	(*successful)++
	mu.Unlock()
}

func archive(name string, level int) error {
	final, err := os.Create(name)
	if err != nil {
		return err
	}

	defer final.Close()

	g, err := gzip.NewWriterLevel(final, level)
	if err != nil {
		return err
	}
	defer g.Close()

	t := tar.NewWriter(g)
	defer t.Close()

	files, err := ioutil.ReadDir(base)
	if err != nil {
		return err
	}

	wd, err := os.Getwd()

	if err != nil {
		return err
	}

	if err := os.Chdir(base); err != nil {
		return err
	}

	defer os.Chdir(wd)

	for _, info := range files {
		err = filepath.Walk(info.Name(), func(file string, i os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			hdr, err := tar.FileInfoHeader(i, file)

			if err != nil {
				return err
			}

			hdr.Name = filepath.ToSlash(file)

			if err := t.WriteHeader(hdr); err != nil {
				return err
			}

			if !i.IsDir() {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				if _, err := io.Copy(t, f); err != nil {
					return err
				}
			}

			return nil
		})

		if err != nil {
			return err
		}
	}

	return nil
}
