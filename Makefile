# ╭────────────────────----------------──────────╮
# │                     go42x                    │
# ╰─────────────────────----------------─────────╯

.PHONY: help
help: Makefile
	@sed -n 's/^##//p' $< | awk 'BEGIN {FS = "|"}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

## setup | install dependencies
setup:
	@go mod tidy -e && go mod download
	@go install go.uber.org/mock/mockgen@latest

## setup-release | install tools for release process
setup-release:
	@go install github.com/goreleaser/goreleaser/v2@latest
	@go install github.com/anchore/syft/cmd/syft@latest
	@go install github.com/sigstore/cosign/v2/cmd/cosign@latest

# ╭────────────────────----------------──────────╮
# │               General workflow               │
# ╰─────────────────────----------------─────────╯

## test | run unit tests
# -count=1 is needed to prevent caching of test results.
test:
	@go test -count=1 -v -race $(shell go list ./... | grep -v './tests')

## build | build development version of binary
build:
	@go build -gcflags="all=-N -l" -race -v -o ./build/go42x .
	@file -h ./build/go42x && du -h ./build/go42x && sha256sum ./build/go42x && go tool buildid ./build/go42x

## generate | generate code for all modules
# Side effects of this command should to be commited.
generate:
	@go mod tidy -e
	@go generate ./...

## clean | clean build artifacts
clean:
	@rm -rf ./build ./dist

# ╭────────────────────----------------──────────╮
# │                   Release                    │
# ╰─────────────────────----------------─────────╯

## release-check | validate goreleaser configuration
release-check:
	@goreleaser --config .goreleaser.yaml check

## release-snapshot | build release artifacts without publishing
release-snapshot:
	@goreleaser --config .goreleaser.yaml release --snapshot --clean --skip=publish,sign

## release-local | test the full release process locally
release-local:
	@goreleaser --config .goreleaser.yaml release --skip=publish --clean
