.PHONY: build test fmt lint check clean_logs

build:
	go build -o rt .

test:
	go test ./...

# Format: gofumpt (stricter gofmt) + goimports (import grouping/pruning).
fmt:
	go tool gofumpt -w .
	go tool goimports -w .

# Lint: go vet + staticcheck.
lint:
	go vet ./...
	go tool staticcheck ./...

# CI-ish gate: check formatting is clean, then build, test and lint.
check:
	@test -z "$$(go tool gofumpt -l .)" || (echo "gofumpt: files need formatting (run 'make fmt')"; go tool gofumpt -l .; exit 1)
	@test -z "$$(go tool goimports -l .)" || (echo "goimports: files need formatting (run 'make fmt')"; go tool goimports -l .; exit 1)
	go build ./...
	go test ./...
	go vet ./...
	go tool staticcheck ./...

clean_logs:
	rm -f rt-*.log