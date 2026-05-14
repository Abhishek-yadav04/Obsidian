// Copyright 2023 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

//go:build tinygo && memoize_builders

package memoize

import "sync"

var doer = newNoSyncDoer(new(sync.Map))

// Do executes and returns the results of the given function, unless there was a cached
// value of the same key. Only one execution is in-flight for a given key at a time.
// The boolean return value indicates whether v was previously stored.
func Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	value, err, _ := doer(key, fn)
	return value, err
}
