// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"github.com/bborbe/github-build-watcher/pkg/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// generateTestPEM produces a fresh 2048-bit RSA PEM block. Generated per-test
// so the key never expires and never collides with anything real.
func generateTestPEM() []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

var _ = Describe("auth.Resolve", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Refusal mode", func() {
		Context("App credentials not configured", func() {
			It("returns nil client and an error mentioning APP_ID", func() {
				httpClient, err := auth.Resolve(ctx, auth.Config{
					LogPrefix: "test",
				})
				Expect(httpClient).To(BeNil())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("APP_ID"))
				Expect(err.Error()).NotTo(ContainSubstring("GH_TOKEN"))
			})
		})
	})

	Describe("Missing PEMKeyFile", func() {
		Context("AppID and InstallationID set but PEMKeyFile does not exist", func() {
			It("returns an error mentioning the missing path", func() {
				_, err := auth.Resolve(ctx, auth.Config{
					AppID:          123456,
					InstallationID: 789012,
					PEMKeyFile:     "/nonexistent/path",
					LogPrefix:      "test",
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(SatisfyAny(
					ContainSubstring("/nonexistent/path"),
					ContainSubstring("no such file"),
				))
			})
		})
	})

	Describe("App mode success", func() {
		Context("all three App fields set with valid PEM", func() {
			It("returns a non-nil http.Client and no error", func() {
				// githubapp.NewClient does no network I/O during construction; the
				// JWT signing setup just validates the PEM parses.
				httpClient, err := auth.Resolve(ctx, auth.Config{
					AppID:          1,
					InstallationID: 2,
					PEMKey:         string(generateTestPEM()),
					LogPrefix:      "test",
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(httpClient).NotTo(BeNil())
			})
		})
	})
})
