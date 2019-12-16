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

var (
	base string
	mu   sync.Mutex

	level      *int
	timeout    *time.Duration
	submodules *bool
)

func usage() {
	fmt.Println(`Usage of gh-dl:
  -l int
        gzip compression level
  -s	recursively fetch submodules
  -t duration
        git clone timeout duration, "0s" for none (default 10m0s)`)
}

func main() {
	name := fmt.Sprintf("gh-dl-%d.tar.gz", time.Now().UTC().Unix())

	log.SetFlags(0)
	log.SetPrefix("fail: ")

	level = flag.Int("l", gzip.DefaultCompression, "gzip compression level")
	timeout = flag.Duration("t", 10*time.Minute,
		`git clone timeout duration, "0s" for none`)
	submodules = flag.Bool("s", false, "recursively fetch submodules")

	flag.Usage = usage
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatal("no username specified")
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
			time.Sleep(250 * time.Millisecond)
		}

		queryWg.Wait()
		dlWg.Done()
	}()

	successful := 0

	go func() {
		for r := range repos {
			go dl(r, &dlWg, &successful)
			time.Sleep(250 * time.Millisecond)
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

	fmt.Println("archiving...")

	if err = archive(name); err != nil {
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
	fmt.Printf("found %d repositories for %s\n", len(st), user)
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

func dl(r repo, dlWg *sync.WaitGroup, successful *int) {
	defer dlWg.Done()

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

func archive(name string) error {
	final, err := os.Create(name)

	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()

	defer final.Close()

	var g *gzip.Writer

	if g, err = gzip.NewWriterLevel(final, *level); err != nil {
		log.Println("gzip level invalid, using default")
		g = gzip.NewWriter(final)
	}
	defer g.Close()

	t := tar.NewWriter(g)
	defer t.Close()

	files, err := ioutil.ReadDir(base)
	if err != nil {
		return err
	}

	for _, info := range files {
		if err = insert(t, info); err != nil {
			return err
		}
	}

	return nil
}

func insert(t *tar.Writer, info os.FileInfo) error {
	full := filepath.Join(base, info.Name())
	cloned, err := ioutil.ReadDir(full)

	if err != nil {
		return err
	}

	if len(cloned) == 0 {
		return nil
	}

	return filepath.Walk(full, func(path string, i os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(base, path)

		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(i, rel)

		if err != nil {
			return err
		}

		hdr.Name = filepath.ToSlash(rel)

		if err := t.WriteHeader(hdr); err != nil {
			return err
		}

		if i.Mode().IsRegular() {
			f, err := os.Open(path)

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
}
