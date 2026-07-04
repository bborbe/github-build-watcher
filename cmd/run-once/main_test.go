// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/bborbe/cqrs/base"
	runonce "github.com/bborbe/github-build-watcher/cmd/run-once"
	"github.com/bborbe/github-build-watcher/mocks"
	"github.com/bborbe/github-build-watcher/pkg"
	libkafka "github.com/bborbe/kafka"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/gexec"
)

// generateTestPEM produces a fresh 2048-bit RSA PEM block for testing.
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

var _ = Describe("Run", func() {
	var (
		ctx         context.Context
		mockWatcher *mocks.Watcher
		app         *runonce.Application
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockWatcher = &mocks.Watcher{}
		app = &runonce.Application{
			KafkaBrokers:    libkafka.Brokers{"localhost:9092"},
			Stage:           "dev",
			RepoAllowlist:   "github.com/owner/repo",
			BuildAssignee:   "test-assignee",
			BuildTaskStatus: "next",
			MaxTitleLen:     200,
			AppID:           1,
			InstallationID:  2,
			PEMKey:          string(generateTestPEM()),
		}
	})

	DescribeTable("error cases",
		func(setupFn func(), expectError bool, errorContains string) {
			mockWatcher.PollReturns(nil)
			mockWatcher.PollStub = nil

			app.CreateWatcher = func(
				ctx context.Context,
				ghClient pkg.GitHubClient,
				brokers libkafka.Brokers,
				topicPrefix base.TopicPrefix,
				inputAllowlist []string,
				resolved pkg.AllowlistSnapshot,
				cursorPath string,
				assignee string,
				taskStatus string,
				taskPhase string,
				maxTitleLen int,
				taskSuffix string,
				currentDateTime libtime.CurrentDateTimeGetter,
			) (pkg.Watcher, libkafka.SyncProducer, func(), error) {
				if len(brokers) == 0 {
					return nil, nil, nil, errors.New("create kafka create sender: brokers empty")
				}
				return mockWatcher, nil, func() {}, nil
			}

			setupFn()

			err := app.Run(ctx, nil)

			if expectError {
				Expect(err).To(HaveOccurred())
				if errorContains != "" {
					Expect(err.Error()).To(ContainSubstring(errorContains))
				}
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("returns error when KAFKA_BROKERS is empty",
			func() {
				app.KafkaBrokers = libkafka.Brokers{}
				mockWatcher.PollReturns(errors.New("should not be called"))
			},
			true,
			"create kafka create sender",
		),
		Entry("returns error when REPO_ALLOWLIST is empty",
			func() {
				app.RepoAllowlist = ""
				mockWatcher.PollReturns(errors.New("should not be called"))
			},
			true,
			"REPO_ALLOWLIST must be non-empty",
		),
		Entry("returns error when Poll fails",
			func() {
				mockWatcher.PollReturns(errors.New("poll failed"))
			},
			true,
			"poll failed",
		),
	)

	Context("success path", func() {
		It("succeeds when all required env vars are set and Poll succeeds", func() {
			mockWatcher.PollReturns(nil)

			app.CreateWatcher = func(
				ctx context.Context,
				ghClient pkg.GitHubClient,
				brokers libkafka.Brokers,
				topicPrefix base.TopicPrefix,
				inputAllowlist []string,
				resolved pkg.AllowlistSnapshot,
				cursorPath string,
				assignee string,
				taskStatus string,
				taskPhase string,
				maxTitleLen int,
				taskSuffix string,
				currentDateTime libtime.CurrentDateTimeGetter,
			) (pkg.Watcher, libkafka.SyncProducer, func(), error) {
				return mockWatcher, nil, func() {}, nil
			}

			err := app.Run(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockWatcher.PollCallCount()).To(Equal(1))
		})
	})
})

var _ = Describe("Main", func() {
	It("Compiles", func() {
		var err error
		_, err = gexec.Build(
			"github.com/bborbe/github-build-watcher/cmd/run-once",
			"-mod=mod",
		)
		Expect(err).NotTo(HaveOccurred())
	})
})

func TestSuite(t *testing.T) {
	time.Local = time.UTC
	format.TruncatedDiff = false
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.Timeout = 60 * time.Second
	RunSpecs(t, "Run-Once Suite", suiteConfig, reporterConfig)
}
