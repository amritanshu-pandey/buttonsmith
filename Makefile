# make            build ./buttonsmith
# make packages   build .deb and .rpm packages for amd64 and arm64 in dist/
# make test

VERSION ?= $(shell git describe --tags --match 'v[0-9]*' 2>/dev/null | sed 's/^v//; s/-/+/; s/-/./g' || true)
ifeq ($(VERSION),)
VERSION := 0.1.0
endif
ARCHES  ?= amd64 arm64
NFPM    ?= go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0
APPID   := io.github.amritanshu_pandey.Buttonsmith
LDFLAGS := -s -w -X github.com/amritanshu-pandey/buttonsmith/internal/app.Version=$(VERSION)

.PHONY: buttonsmith test packages clean

buttonsmith:
	go build -ldflags '$(LDFLAGS)' -o $@ ./cmd/buttonsmith

test:
	go test ./...

packages: test
	mkdir -p dist
	sed 's|@BIN@|/usr/bin/buttonsmith|' contrib/buttonsmith.service > dist/buttonsmith.service
	sed 's|@BIN@|/usr/bin/buttonsmith|' contrib/$(APPID).desktop > dist/$(APPID).desktop
	for arch in $(ARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o dist/bin/buttonsmith ./cmd/buttonsmith || exit 1; \
		for fmt in deb rpm; do \
			ARCH=$$arch VERSION=$(VERSION) $(NFPM) package -f packaging/nfpm.yaml -p $$fmt -t dist/ || exit 1; \
		done; \
	done
	ls -l dist/*.deb dist/*.rpm

clean:
	rm -rf dist buttonsmith
