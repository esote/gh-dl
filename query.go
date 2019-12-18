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
	"strings"
	"sync"
	"time"
)

type found struct {
	found uint64
	name  string
}

type user []struct {
	Name   string `json:"full_name"`
	GitURL string `json:"git_url"`
}

func fanQueries(repos chan<- repo, dlWg *sync.WaitGroup, total *uint64, args []string) {
	var wg sync.WaitGroup
	var names []string

	u := &url.URL{
		Scheme: "git",
		Host:   "github.com",
	}
	for _, arg := range args {
		split := strings.Split(arg, "/")
		switch len(split) {
		case 1:
			names = append(names, arg)
		case 2:
			if err := mkdir(split[0]); err != nil {
				log.Println(err)
				continue
			}

			u.Path = arg + ".git"
			dlWg.Add(1)
			*total++
			repos <- repo{
				git:  u.String(),
				name: arg,
				user: split[0],
			}
		default:
			log.Println("argument", arg, "invalid")
		}
	}

	f := make(chan found)
	go printFound(f, total)

	wg.Add(len(names))
	for _, name := range names {
		go func(name string) {
			defer wg.Done()
			count, err := query(name, repos, dlWg)

			if err == nil {
				f <- found{count, name}
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

func query(name string, repos chan<- repo, wg *sync.WaitGroup) (uint64, error) {
	if err := mkdir(name); err != nil {
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

	for page, pages := 1, 1; page <= pages; page++ {
		v.Set("page", strconv.Itoa(page))
		api.RawQuery = v.Encode()

		var users user
		users, pages, err = getRepos(api.String(), pages)

		if err != nil {
			return 0, err
		}

		if len(users) == 0 {
			break
		}

		count += len(users)

		wg.Add(len(users))
		for _, r := range users {
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

func getRepos(url string, pages int) (u user, count int, err error) {
	resp, err := http.Get(url)

	if err != nil {
		return
	}

	if err = checkRateLimit(resp.Header); err != nil {
		return
	}

	var ok bool
	if count, ok = pageCount(resp.Header.Get("Link")); !ok {
		count = pages
	}

	body, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		return
	}

	defer resp.Body.Close()

	err = json.Unmarshal(body, &u)
	return
}

func printFound(found <-chan found, total *uint64) {
	for f := range found {
		fmt.Printf("found %d repositories for %s\n", f.found, f.name)
		*total += f.found
	}
}

func checkRateLimit(hdrs http.Header) error {
	if hdrs.Get("X-RateLimit-Remaining") != "0" {
		return nil
	}

	reset, err := strconv.ParseInt(hdrs.Get("X-RateLimit-Remaining"), 10, 64)

	if err != nil {
		return errors.New("rate limit exceeded")
	}

	return fmt.Errorf("rate limit exceeded, will reset %s",
		time.Unix(reset, 0).String())
}

func pageCount(str string) (int, bool) {
	links := strings.Split(str, ", ")

	if len(links) == 0 {
		return 0, false
	}

	// "last" link is usually at the end so traverse in reverse
	for i := len(links) - 1; i > 0; i-- {
		parts := strings.Split(links[i], "; ")

		if len(parts) != 2 || parts[1] != `rel="last"` || len(parts[0]) < 3 {
			continue
		}

		link, err := url.Parse(parts[0][1 : len(parts[0])-2])

		if err != nil {
			return 0, false
		}

		n, err := strconv.Atoi(link.Query().Get("page"))
		return n, err == nil
	}

	return 0, false
}

func mkdir(name string) error {
	err := os.Mkdir(filepath.Join(base, name), 0700)

	if err == nil || os.IsExist(err) {
		return nil
	}

	return err
}
