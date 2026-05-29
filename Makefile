.PHONY: build test fmt lint clean run release release-snapshot man

BINARY := emailable
PKG    := .
PLUGIN := .claude-plugin/plugin.json

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

# Cut a release: sync the plugin manifest version, commit, and tag.
# Usage: make release VERSION=1.2.3
# Pushing the tag triggers the GoReleaser workflow, which also verifies the
# tag matches plugin.json (see .github/workflows/release.yml).
release:
ifndef VERSION
	$(error VERSION is required, e.g. make release VERSION=1.2.3)
endif
	@echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "error: VERSION must be semver x.y.z (got '$(VERSION)')"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "error: working tree not clean; commit or stash first"; exit 1; }
	@tmp=$$(mktemp) && sed -E 's/("version": *")[^"]*(")/\1$(VERSION)\2/' $(PLUGIN) > $$tmp && mv $$tmp $(PLUGIN)
	@git add $(PLUGIN)
	@git commit -m "Release v$(VERSION)"
	@git tag -a v$(VERSION) -m "v$(VERSION)"
	@echo "Tagged v$(VERSION). Push the commit and tag to release: git push --follow-tags"

release-snapshot:
	goreleaser release --snapshot --clean

man: build
	mkdir -p man
	./bin/$(BINARY) man -o man
