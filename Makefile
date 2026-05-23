# meet Makefile.
#
# Local dev targets only. Deployment is handled by the hetzner repo's
# deploy-app command.

APP          := meet
MAIN_PKG     := ./cmd/meet
BUILD_OUTPUT := ./bin/$(APP)
CONFIG_PATH  := config/localhost.yaml
SECRETS_PATH := secrets/localhost.yaml

.PHONY: build test test-one-off lint clean install uninstall init serve sync release

build:
	@echo "==> building $(APP) for $$(go env GOOS)/$$(go env GOARCH)"
	@mkdir -p $(dir $(BUILD_OUTPUT))
	go build -o $(BUILD_OUTPUT) $(MAIN_PKG)
	go build -o ./bin/meet-helper ./cmd/meet-helper
	@ls -la $(BUILD_OUTPUT) ./bin/meet-helper

test: lint
	@echo "==> running regression tests"
	@if ! find tests/regression -name '*_test.go' -print -quit 2>/dev/null | grep -q .; then \
	  echo "WARNING: no regression tests found in tests/regression/"; \
	  exit 1; \
	fi
	go test ./tests/regression/... -v

test-one-off:
ifdef ISSUE
	go test ./tests/one_off/... -v -run "$(ISSUE)"
else
	go test ./tests/one_off/... -v
endif

lint:
	@echo "==> linting"
	go vet ./...

clean:
	rm -rf ./bin

install: build
	ln -sfn $(CURDIR)/$(BUILD_OUTPUT) $(HOME)/.local/bin/$(APP)
	ln -sfn $(CURDIR)/bin/meet-helper $(HOME)/.local/bin/meet-helper

uninstall:
	rm -f $(HOME)/.local/bin/$(APP)
	rm -f $(HOME)/.local/bin/meet-helper

serve: build
	CONFIG_PATH=$(CONFIG_PATH) SECRETS_PATH=$(SECRETS_PATH) $(BUILD_OUTPUT)

token: build
	CONFIG_PATH=$(CONFIG_PATH) SECRETS_PATH=$(SECRETS_PATH) $(BUILD_OUTPUT) token --room $(ROOM)

init:
	@if [ ! -f config/localhost.yaml ]; then \
	  cp config/localhost.yaml.example config/localhost.yaml; \
	  echo "Created config/localhost.yaml — edit it to set your vpaas_id"; \
	fi
	@if [ ! -f secrets/localhost.env ]; then \
	  cp secrets/env.example secrets/localhost.env; \
	fi
	@if [ ! -L .env ]; then \
	  ln -sf secrets/localhost.env .env; \
	fi
	@if ls hooks/* >/dev/null 2>&1; then \
	  for hook in hooks/*; do \
	    cp "$$hook" .git/hooks/; \
	    chmod +x ".git/hooks/$$(basename "$$hook")"; \
	  done; \
	  echo "Installed project hooks"; \
	fi
	@echo "==> init complete"

sync:
	git add --all
	git commit -m "sync: $$(date -u +%Y-%m-%dT%H:%M:%SZ)" || true
	git pull
	git push

release:
ifndef SKIP_TESTS
	$(MAKE) test
endif
	@current=$$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0"); \
	major=$$(echo "$$current" | sed 's/^v//' | cut -d. -f1); \
	minor=$$(echo "$$current" | sed 's/^v//' | cut -d. -f2); \
	next="v$$major.$$((minor + 1))"; \
	echo "==> tagging $$next (was $$current)"; \
	git tag "$$next"; \
	git push origin "$$next"
