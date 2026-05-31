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
	@dir="$(CURDIR)/bin"; \
	for f in "$$HOME/.profile" "$$HOME/.bashrc" "$$HOME/.zshrc"; do \
	  if grep -qsF "$$dir" "$$f" 2>/dev/null; then \
	    echo "Already in $$f"; \
	  else \
	    printf 'export PATH="$$PATH:%s"\n' "$$dir" >> "$$f"; \
	    echo "Added to $$f"; \
	  fi; \
	done; \
	echo "wasa is on PATH for sh, bash and zsh. Open a NEW terminal, then run: wasa"
endif
