# Development

## Building

`go build` (Go 1.24+). Cross-compile: `GOOS=windows go build`.

## CI

Every push to `main` and every pull request runs `gofmt`, `go vet`, `go test` and `go test -race`, and
builds Linux, macOS and Windows binaries (amd64 and arm64). The check job installs Node and asserts that
it is there, so the viewer's JavaScript tests run instead of skipping; a separate job installs KiCad and
runs the tests with `DREIECKI_REQUIRE_KICAD=1`, so the KiCad-gated tests fail instead of skipping. The
binaries are uploaded as workflow artifacts, versioned `dev-<sha>`. Changes that only touch Markdown,
`docs/` or `.gitignore` skip CI.

## Releases

To publish a GitHub release, tag a commit and push the tag:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The tag name becomes the version (shown by `-version`) and the release title. The workflow only starts
for tags matching the trigger pattern `v[0-9]*.[0-9]*.[0-9]*`; a tag outside it does not start the
workflow at all. The `version` job then refuses a tag that does not match
`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?([+][0-9A-Za-z.-]+)?$` and fails the build, so `v0.2.0`,
`v0.2.0-rc1` and `v0.2.0-rc.1+build.5` are fine while `v0.2` and `v0.2.0.4` are not.

The release notes list the pull requests merged since the previous version tag, as GitHub generates
them, followed by "Other changes": every commit pushed straight to `main` or merged without a pull
request. Preview them with `.github/release-notes.sh uberrice/dreiecki vX.Y.Z $(git rev-parse HEAD)`
(needs `gh`; the commit must already be pushed).

The `release` job does not wait for the KiCad job, because that job installs KiCad from a third-party
PPA; check that it passed before tagging.
