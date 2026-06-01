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
	@powershell -NoProfile -Command "$$d=(Resolve-Path '$(CURDIR)/bin').Path; $$shadow='$(GOPATH_FWD)/bin/wasa.exe'; if (Test-Path $$shadow) { Remove-Item $$shadow -Force; Write-Host ('Removed stale shadow build: ' + $$shadow) }; $$u=[Environment]::GetEnvironmentVariable('Path','User'); $$parts=@($$u -split ';' | Where-Object { $$_ -ne '' -and $$_ -ne $$d }); [Environment]::SetEnvironmentVariable('Path', ((@($$d)+$$parts) -join ';'), 'User'); Write-Host ('Fresh wasa at ' + $$d + ' is now first on your user PATH.'); $$others=Get-Command wasa -All -ErrorAction SilentlyContinue | Where-Object { $$_.Source -and (Split-Path $$_.Source) -ne $$d }; foreach ($$o in $$others) { Write-Host ('WARNING: another wasa may shadow this build, delete it: ' + $$o.Source) }; Write-Host 'Open a NEW terminal, then run: wasa'"
else
env: build
	@dir="$(CURDIR)/bin"; \
	shadow="$(GOPATH_FWD)/bin/wasa"; \
	if [ -f "$$shadow" ]; then rm -f "$$shadow"; echo "Removed stale shadow build: $$shadow"; fi; \
	for f in "$$HOME/.profile" "$$HOME/.bashrc" "$$HOME/.zshrc"; do \
	  if grep -qsF "wasa-env:$$dir" "$$f" 2>/dev/null; then \
	    echo "Already prepended in $$f"; \
	  else \
	    printf '\n# wasa-env:%s\nexport PATH="%s:$$PATH"\n' "$$dir" "$$dir" >> "$$f"; \
	    echo "Prepended to $$f"; \
	  fi; \
	done; \
	other="$$(command -v wasa 2>/dev/null || true)"; \
	if [ -n "$$other" ] && [ "$$other" != "$$dir/wasa" ]; then \
	  echo "WARNING: another wasa may shadow this build, delete it: $$other"; \
	fi; \
	echo "Fresh wasa is first on PATH for sh, bash and zsh. Open a NEW terminal, then run: wasa"
endif
