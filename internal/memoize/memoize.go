// Copyright 2023 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package memoize

import (
	"sync"

	"golang.org/x/sync/singleflight"
)

type doerFunc func(key string, fn func() (any, error)) (any, error, bool)

func newDoer(cache *sync.Map, group *singleflight.Group) doerFunc {
	return func(key string, fn func() (any, error)) (any, error, bool) {
		// Check cache
		value, found := cache.Load(key)
		if found {
			return value, nil, true
		}

		// Combine memoized function with a cache store
		value, err, _ := group.Do(key, func() (any, error) {
			data, innerErr := fn()
			if innerErr == nil {
				cache.Store(key, data)
			}

			return data, innerErr
		})

		return value, err, false
	}
}

func newNoSyncDoer(cache *sync.Map) doerFunc {
	return func(key string, fn func() (any, error)) (any, error, bool) {
		// Check cache
		value, found := cache.Load(key)
		if found {
			return value, nil, true
		}

		data, err := fn()
		if err == nil {
			cache.Store(key, data)
		}

		return data, err, false
	}
}
