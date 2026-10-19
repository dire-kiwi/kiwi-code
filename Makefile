.PHONY: build dev dev\:desktop headless-test run run\:desktop test web web-deps

DEV_ARGS ?=

# Installs web/node_modules exactly as locked with `npm ci`, but only when
# package.json or package-lock.json changed since the last successful install,
# so routine builds and restarts neither rewrite the lockfile nor need the
# network. The stamp lives inside node_modules, which `npm ci` recreates, so an
# interrupted or failed install is retried next time. Runs from web/.
WEB_DEPS_STAMP := node_modules/.kiwi-code-deps.sha256
WEB_DEPS_HASH := node -e "const crypto = require('node:crypto'), fs = require('node:fs'); process.stdout.write(crypto.createHash('sha256').update(fs.readFileSync('package.json')).update(fs.readFileSync('package-lock.json')).digest('hex'))"
WEB_DEPS := hash="$$($(WEB_DEPS_HASH))" && \
	if [ "$$(cat $(WEB_DEPS_STAMP) 2>/dev/null)" != "$$hash" ]; then \
		npm ci && printf '%s\n' "$$hash" > $(WEB_DEPS_STAMP); \
	fi

web-deps:
	@cd web && $(WEB_DEPS)

web: web-deps
	cd web && npm run build

build: web
	mkdir -p bin
	go build -o bin/kiwi-code .

dev: web-deps
	cd web && npm run dev:servers -- $(DEV_ARGS)

dev\:desktop: web-deps
	cd web && npm run dev:desktop -- $(DEV_ARGS)

# Exit status 0 from the server is a restart request. A failed reinstall or
# rebuild (for example while offline) keeps serving the previous frontend build
# instead of leaving the server down; it only stops when no build exists yet.
run: web
	@while true; do \
		KIWI_CODE_ADDR="$${KIWI_CODE_ADDR:-0.0.0.0:4000}" go run .; \
		status=$$?; \
		if [ "$$status" -ne 0 ]; then exit "$$status"; fi; \
		printf '%s\n' 'kiwi-code restart requested; rebuilding and starting a fresh instance...'; \
		(cd web && $(WEB_DEPS) && npm run build); \
		status=$$?; \
		if [ "$$status" -ne 0 ]; then \
			if [ ! -f internal/server/static/app/index.html ]; then exit "$$status"; fi; \
			printf '%s\n' "kiwi-code frontend rebuild failed (exit $$status); starting with the previous build..." >&2; \
		fi; \
	done

run\:desktop: web
	cd web && KIWI_CODE_BROWSER_BACKEND=electron npm run run:desktop

headless-test:
	go run ./cmd/headless-client

test: web-deps
	go test ./...
	go vet ./...
	cd web && npm test && npm run build
