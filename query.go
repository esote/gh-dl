package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type found struct {
	found uint64
	name  string
}

func fanQueries(repos chan<- repo, dlWg *sync.WaitGroup, total *uint64, args []string) {
	var wg sync.WaitGroup

	f := make(chan found, len(args))
	go printFound(f, total)

	wg.Add(len(args))

	for _, name := range args {
		go func(name string) {
			defer wg.Done()
			count, err := query(name, repos, dlWg)

			if err == nil {
				f <- found{found: count, name: name}
			} else {
				log.Println(err)
			}
		}(name)
		time.Sleep(sleep)
	}

	wg.Wait()
	close(f)
	dlWg.Done()
}

func query(name string, repos chan<- repo, dlWg *sync.WaitGroup) (uint64, error) {
	if err := os.Mkdir(filepath.Join(base, name), 0700); err != nil {
		return 0, err
	}

	api, err := url.Parse("https://api.github.com/users/")

	if err != nil {
		return 0, err
	}

	api.Path = filepath.Join(api.Path, name, "repos")

	v := url.Values{}
	v.Add("page_size", "100")

	var count int

	for page := uint64(1); ; page++ {
		v.Set("page", strconv.FormatUint(page, 10))
		api.RawQuery = v.Encode()

		resp, err := http.Get(api.String())

		if err != nil {
			return 0, err
		}

		if err = checkRateLimit(resp); err != nil {
			return 0, err
		}

		data, err := ioutil.ReadAll(resp.Body)

		if err != nil {
			return 0, err
		}

		if err = resp.Body.Close(); err != nil {
			return 0, err
		}

		var st []struct {
			Name   string `json:"full_name"`
			GitURL string `json:"git_url"`
		}

		if err := json.Unmarshal(data, &st); err != nil {
			return 0, err
		}

		if len(st) == 0 {
			break
		}

		dlWg.Add(len(st))
		count += len(st)

		for _, r := range st {
			repos <- repo{
				git:  r.GitURL,
				name: r.Name,
				user: name,
			}
		}

		time.Sleep(sleep)
	}

	return uint64(count), nil
}

func printFound(found <-chan found, total *uint64) {
	for f := range found {
		fmt.Printf("found %d repositories for %s\n", f.found, f.name)
		*total += f.found
	}
}

func checkRateLimit(resp *http.Response) error {
	if resp.Header.Get("X-RateLimit-Remaining") != "0" {
		return nil
	}

	reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Remaining"), 10, 64)

	if err != nil {
		return errors.New("rate limit exceeded")
	}

	return fmt.Errorf("rate limit exceeded, will reset %s",
		time.Unix(reset, 0).String())
}
