# Scripts

## verify.sh

Preferred repo-local verification entrypoint.
Runs `go test ./...` and `go build ./...` with a temporary Go build cache under `/tmp` so verification does not depend on the default macOS cache path.
