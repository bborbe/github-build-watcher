// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bborbe/github-build-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewGitHubClient concurrency bound", func() {
	It("never exceeds githubAPIInflightBound in flight", func() {
		var inFlight, peak int64
		var mu sync.Mutex

		// A slow inner transport is what makes concurrency observable: without
		// the delay each call would complete before the next starts and the
		// peak would read 1 regardless of the limiter.
		slow := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			cur := atomic.AddInt64(&inFlight, 1)
			mu.Lock()
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("{}")),
				Header:     http.Header{},
				Request:    req,
			}, nil
		})

		httpClient := &http.Client{Transport: slow}
		// The real production constructor, so the limiter is the one the watcher
		// actually ships rather than a hand-built one.
		pkg.NewGitHubClient(httpClient, nil, "")

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/o/r", nil)
				Expect(err).To(BeNil())
				_, _ = httpClient.Transport.RoundTrip(req)
			}()
		}
		wg.Wait()

		mu.Lock()
		observed := peak
		mu.Unlock()

		Expect(observed).To(BeNumerically("<=", 4))
		Expect(observed).To(BeNumerically(">=", 2))
	})
})
