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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

func archive(base, name string) error {
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

	if g, err = gzip.NewWriterLevel(final, level); err != nil {
		msgs <- msg{
			s: "gzip level invalid, using default",
			v: true,
		}
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
		if err = insert(base, t, info); err != nil {
			return err
		}
	}

	return nil
}

func insert(base string, t *tar.Writer, info os.FileInfo) error {
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
