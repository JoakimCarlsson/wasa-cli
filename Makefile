.PHONY: install fmt lint build run

GOPATH_FWD := $(subst \,/,$(shell go env GOPATH))

ifeq ($(OS),Windows_NT)
    GOLANGCI := cmd /c "set GOTOOLCHAIN=local&& golangci-lint run ./..."
else
    GOLANGCI := GOTOOLCHAIN=local $(GOPATH_FWD)/bin/golangci-lint run ./...
endif

install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golangci/golines@latest

fmt:
	$(GOPATH_FWD)/bin/goimports -w .
	$(GOPATH_FWD)/bin/golines -m 80 -w .

lint:
	go vet ./...
	$(GOLANGCI)

build:
	go build -buildvcs=false -o /tmp/wasa ./cmd/wasa

run: build
	@WASA_HOME=$${WASA_HOME:-$$(mktemp -d)}; \
	echo "Launching wasa (WASA_HOME=$$WASA_HOME) in $$(pwd)"; \
	WASA_HOME=$$WASA_HOME exec /tmp/wasa
