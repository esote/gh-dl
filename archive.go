package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
)

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

	walk := func(path string, i os.FileInfo, err error) error {
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
	}

	return filepath.Walk(full, walk)
}
