# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- Update Go to 1.26.6 and update dependencies

## v0.1.4

- chore: Update Go to 1.26.6 and update dependencies
- fix: Fix GO-2026-6179, GO-2026-6180, CVE-2026-56864, CVE-2026-56865 (golang.org/x/mod transparency log tile verification bypass and hash verification issues)
- fix: Fix GO-2026-5026, GO-2026-5972, GO-2026-6089, GO-2026-6090, GO-2026-6218 (stdlib vulnerabilities via Go directive bump)
## v0.1.3

- Update Go to 1.26.6 and update dependencies (golang.org/x/mod to v0.40.0 addressing GO-2026-6179, GO-2026-6180, CVE-2026-56864, CVE-2026-56865; stdlib CVEs addressed via Go 1.26.6)

## v0.1.2

- Update Go dependencies (agent, cqrs, errors, http, kafka, kv, log, maintainer, parse, run, sentry, service, time)
- Bump Docker base images to golang:1.26.5 and alpine:3.24
- Add vulncheck ignore for GO-2026-5932 (no-fix advisory)
- Fix README links and paths for standalone repo layout

## v0.1.1

- refactor: import the shared library from its new root module path `github.com/bborbe/maintainer` (was `github.com/bborbe/maintainer/lib`) and bump to `@v0.45.0`. The maintainer repo flattened `lib/` to its root to match the `bborbe/agent` layout. No behavior change.

## v0.1.0

- Extracted from the `bborbe/maintainer` monorepo (`watcher/github-build`) into a standalone
  publish-only repository. Shared code now comes from the versioned
  `github.com/bborbe/maintainer/lib` module instead of a local `replace`. Builds and
  publishes `docker.io/bborbe/github-build-watcher:<version>` via `make buca`.
