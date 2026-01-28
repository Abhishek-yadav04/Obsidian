// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package operators

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	iio "github.com/corazawaf/coraza/v3/internal/io"
)

var errEmptyDirs = errors.New("empty dirs")

func loadFromFile(fname string, dirs []string, root fs.FS) ([]byte, error) {
	// Detect absolute paths using OS-aware filepath package
	if filepath.IsAbs(fname) {
		// If root is the OSFS we can use the OS-style absolute path directly
		if _, ok := root.(iio.OSFS); ok {
			return fs.ReadFile(root, fname)
		}
		// For other fs.FS implementations (embed.FS) convert to posix-style
		return fs.ReadFile(root, filepath.ToSlash(fname))
	}

	if len(dirs) == 0 {
		return nil, errEmptyDirs
	}

	// handling files by operators is hard because we must know the paths where we can
	// search, for example, the policy path or the binary path...
	// CRS stores the .data files in the same directory as the directives
	var (
		content []byte
		err     error
	)

	for _, p := range dirs {
		var absFilepath string
		if _, ok := root.(iio.OSFS); ok {
			absFilepath = filepath.Join(p, fname)
		} else {
			// non-OS FS (embed.FS) expects forward slashes
			absFilepath = path.Join(p, fname)
			absFilepath = filepath.ToSlash(absFilepath)
		}
		content, err = fs.ReadFile(root, absFilepath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			} else {
				return nil, err
			}
		}

		return content, nil
	}

	return nil, err
}
