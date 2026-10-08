# Development

## Building

`go build` (Go 1.27.1+). Cross-compile: `GOOS=windows go build`.

## CI

Every push to `main` and every pull request runs `gofmt`, `go vet` and `go test`, and builds Linux,
macOS and Windows binaries (amd64 and arm64). The binaries are uploaded as workflow artifacts,
versioned `dev-<sha>`. Changes that only touch Markdown, `docs/` or `.gitignore` skip CI.

## Releases

To publish a GitHub release, tag a commit and push the tag:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The tag name becomes the version (shown by `-version`) and the release title.
