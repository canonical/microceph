pkg_version=$(shell apt-cache policy ceph-common | awk '/Candidate:/{ print $$2 }' )
git_version=$(shell git describe --always --dirty --abbrev=10)
MC_VERSION=ceph-version: $(pkg_version); microceph-git: $(git_version)
LDFLAGS="-X 'github.com/canonical/microceph/microceph/version.version=$(MC_VERSION)'"

.PHONY: default
default: build

# Build targets.
.PHONY: build
build:
	go install -v -ldflags $(LDFLAGS) ./cmd/microceph
	go install -v -ldflags $(LDFLAGS) ./cmd/microcephd

# Testing targets.
.PHONY: check
check: check-static check-unit check-system

.PHONY: check-unit
check-unit:
	go test ./...

.PHONY: check-system
check-system:
	true

.PHONY: check-static
check-static:
ifeq ($(shell command -v golangci-lint 2> /dev/null),)
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
endif
ifeq ($(shell command -v shellcheck 2> /dev/null),)
	echo "Please install shellcheck"
	exit 1
endif
ifeq ($(shell command -v revive 2> /dev/null),)
	go install github.com/mgechev/revive@latest
endif
	golangci-lint run --timeout 5m
	revive -set_exit_status ./...

# Update targets.
.PHONY: update-gomod
update-gomod:
	go get -u ./...

	# Static pins
	go get github.com/canonical/lxd@stable-5.21 # Stay on v2 dqlite and LXD LTS client

	go mod tidy
	go get toolchain@none

# Update lxd-generate generated database helpers.
.PHONY: update-schema
update-schema:
	go generate ./...
	gofmt -s -w ./database/
	goimports -w ./database/
	@echo "Code generation completed"

