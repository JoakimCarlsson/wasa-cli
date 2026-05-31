.PHONY: install fmt lint build run env

GOPATH_FWD := $(subst \,/,$(shell go env GOPATH))

ifeq ($(OS),Windows_NT)
    GOLANGCI := cmd /c "set GOTOOLCHAIN=local&& golangci-lint run ./..."
    BIN := bin/wasa.exe
    RUN := .\bin\wasa.exe
else
    GOLANGCI := GOTOOLCHAIN=local $(GOPATH_FWD)/bin/golangci-lint run ./...
    BIN := bin/wasa
    RUN := ./bin/wasa
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
	go build -buildvcs=false -o $(BIN) ./cmd/wasa

run: build
	$(RUN)

ifeq ($(OS),Windows_NT)
env: build
	@powershell -NoProfile -Command "$$d=(Resolve-Path '$(CURDIR)/bin').Path; $$u=[Environment]::GetEnvironmentVariable('Path','User'); if (($$u -split ';') -notcontains $$d) { [Environment]::SetEnvironmentVariable('Path', ($$u.TrimEnd(';') + ';' + $$d), 'User'); Write-Host ('Added ' + $$d + ' to your user PATH.') } else { Write-Host ('Already on PATH: ' + $$d) }; Write-Host 'Open a NEW terminal, then run: wasa'"
else
env: build
	@dir="$(CURDIR)/bin"; prof="$$HOME/.profile"; \
	if echo ":$$PATH:" | grep -q ":$$dir:"; then \
	  echo "Already on PATH: $$dir"; \
	elif grep -qsF "$$dir" "$$prof" 2>/dev/null; then \
	  echo "Already in $$prof: $$dir"; \
	else \
	  printf 'export PATH="$$PATH:%s"\n' "$$dir" >> "$$prof"; \
	  echo "Added $$dir to $$prof. Open a NEW terminal (or run: source $$prof), then run: wasa"; \
	fi
endif
