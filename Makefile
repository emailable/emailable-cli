.PHONY: build test fmt lint clean run release-snapshot man

BINARY := emailable
PKG    := .

build:
	go build -o bin/$(BINARY) $(PKG)

test:
	go test -race -coverprofile=coverage.txt ./...

fmt:
	gofmt -w .

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ dist/
	find . -type f -name 'coverage.txt' -delete

run:
	go run $(PKG) $(ARGS)

release-snapshot:
	goreleaser release --snapshot --clean

man: build
	mkdir -p man
	./bin/$(BINARY) man -o man
