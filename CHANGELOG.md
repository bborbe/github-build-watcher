# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
