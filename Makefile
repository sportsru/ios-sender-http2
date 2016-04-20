.PHONY: all build test lint vet fmt travis coverage checkfmt prepare updep tools extra-tools generate
# Usage example:
# 1) make updep 
# 2) RACE_FLAG=-race BIN_VERSION=2.0-prerelease make install
#
# development flow example:
# 1) make tools
#    make extra-tools
# 2) make generate
# 3) make all

PKGSDIRS=$(shell find -L . -type f -name "*.go" -not -path "./vendor/*" -not -name '*_string.go')
GIT_VERSION ?= $(shell git describe --exact-match --tags HEAD 2>/dev/null)
GIT_VERSION += $(shell git log --pretty=format:'%h' -n 1)
BIN_VERSION := $(shell echo '${GIT_VERSION}' | tr ' ' '-')

LDFLAGS := -ldflags="-X main.VERSION=${BIN_VERSION}"

GO_ENV := GO15VENDOREXPERIMENT=1

BUILD_FLAGS := ${RACE_FLAG} ${LDFLAGS}

DISABLED_LINTERS := -Dgotype -Ddeadcode -Daligncheck -Dstructcheck

install: Makefile
	${GO_ENV} go install -v ${BUILD_FLAGS} github.com/sportsru/ios-sender

generate:
	go generate

check-all: test vet checkfmt

prepare: dep-update fmt test vet checkfmt

test:
	@echo "Test packages"
	${GO_ENV} go test $(glide novendor)

lint:
	@echo "Run lint"
	test -z "$$(golint ./... | grep -v Godeps/_workspace/src/ | tee /dev/stderr)"

vet:
	@echo "Run vet"
	@go vet $(glide novendor)

checkfmt:
	@echo "Check formats"
	@./bin/checkfmt.sh .

fmt:
	@echo "Formatting"
	@echo $(PKGSDIRS) | xargs -I '{p}' -n1 goimports -w {p}

tools-all: tools-required extra-tools

tools-required:
	@echo "Install/update tools"
	go get -u golang.org/x/tools/cmd/stringer
	go get -u golang.org/x/tools/cmd/goimports
	go get -u github.com/golang/lint

lint-all:
	GO_VENDOR=1 gometalinter ${DISABLED_LINTERS} -e '^apnserrorcode_string'

tools-extra:
	go get -u github.com/alecthomas/gometalinter
	gometalinter --install

tools-extra-update:
	gometalinter --install --update

dep-update:
	@echo "Save dependencies"
	${GO_ENV} glide update

