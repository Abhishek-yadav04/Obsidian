// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !tinygo

package auditlog

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
)

type concurrentWriter struct {
	mux         *sync.RWMutex
	log         *log.Logger
	logDir      string
	logDirMode  fs.FileMode
	logFileMode fs.FileMode
	formatter   plugintypes.AuditLogFormatter
	io.Closer
	targetPath  string
}

func (cl *concurrentWriter) Init(c plugintypes.AuditLogConfig) error {
	if c.Target == "" {
		cl.Closer = NoopCloser
		return nil
	}

	cl.logFileMode = c.FileMode
	cl.logDir = c.Dir
	cl.logDirMode = c.DirMode
	cl.formatter = c.Formatter
	cl.mux = &sync.RWMutex{}

	// Store target path but avoid keeping the file handle open across test boundaries
	// to allow temp dirs to be removed on Windows. We'll open/append for each log
	// entry when writing instead of holding the file descriptor open.
	// Validate target is writable now (but close immediately) so Init fails
	// for invalid targets as tests expect.
	switch c.Target {
	case os.DevNull, "/dev/stdout", "/dev/stderr":
		// supported virtual targets, nothing to validate
	default:
		f, err := os.OpenFile(c.Target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, c.FileMode)
		if err != nil {
			return err
		}
		_ = f.Close()
	}
	cl.targetPath = c.Target
	cl.Closer = NoopCloser
	// use a no-op logger; writes will append directly to the target file
	cl.log = log.New(io.Discard, "", 0)
	return nil
}

func (cl concurrentWriter) Write(al plugintypes.AuditLog) error {
	if cl.formatter == nil {
		return nil
	}

	formattedAL, err := cl.formatter.Format(al)
	if err != nil {
		return err
	}

	if len(formattedAL) == 0 {
		return nil
	}

	// 192.168.3.130 192.168.3.1 - - [22/Aug/2009:13:24:20 +0100] "GET / HTTP/1.1" 200 56 "-" "-" SojdH8AAQEAAAugAQAAAAAA "-" /20090822/20090822-1324/20090822-132420-SojdH8AAQEAAAugAQAAAAAA 0 1248
	t := time.Unix(0, al.Transaction().UnixTimestamp())

	ymd := t.Format("20060102")
	ymdhm := ymd + t.Format("-1504")
	filename := ymdhm + t.Format("05") + "-" + al.Transaction().ID()

	logdir := path.Join(cl.logDir, ymd, ymdhm)
	if err := os.MkdirAll(logdir, cl.logDirMode); err != nil {
		return err
	}

	filepath := path.Join(logdir, filename)
	if err = os.WriteFile(filepath, formattedAL, cl.logFileMode); err != nil {
		return err
	}

	// Append a human readable summary line to the configured target file.
	// Open/append/close per write so we don't keep the file locked across tests on Windows.
	cl.mux.Lock()
	defer cl.mux.Unlock()

	if cl.targetPath != "" {
		f, err := os.OpenFile(cl.targetPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, cl.logFileMode)
			if err == nil {
			// compose the same lines the logger used to print
			// client host info + request line + status + id + path
			// we use Printf-like formatting into a single byte slice
			var b []byte
			b = append(b, []byte(fmt.Sprintf("%s %s - - [%s]", al.Transaction().ClientIP(), al.Transaction().HostIP(), al.Transaction().Timestamp()))...)
			if al.Transaction().HasRequest() {
				b = append(b, []byte(fmt.Sprintf(" \"%s %s %s\"", al.Transaction().Request().Method(), al.Transaction().Request().URI(), al.Transaction().Request().HTTPVersion()))...)
			}
			if al.Transaction().HasResponse() {
				b = append(b, []byte(fmt.Sprintf(" %d", al.Transaction().Response().Status()))...)
			}
			b = append(b, []byte(fmt.Sprintf(" %s - %s\n", al.Transaction().ID(), filepath))...)

			// write and close
			_, _ = f.Write(b)
			_ = f.Close()
		}
	}

	return nil
}

var _ plugintypes.AuditLogWriter = (*concurrentWriter)(nil)
