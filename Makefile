.PHONY: build dev dev\:desktop headless-test run run\:desktop test web

DEV_ARGS ?=

web:
	cd web && npm install && npm run build

build: web
	mkdir -p bin
	go build -o bin/kiwi-code .

dev:
	cd web && npm install && npm run dev:servers -- $(DEV_ARGS)

dev\:desktop:
	cd web && npm install && npm run dev:desktop -- $(DEV_ARGS)

run: web
	@while true; do \
		KIWI_CODE_ADDR="$${KIWI_CODE_ADDR:-0.0.0.0:4000}" go run .; \
		status=$$?; \
		if [ "$$status" -ne 0 ]; then exit "$$status"; fi; \
		printf '%s\n' 'kiwi-code restart requested; rebuilding and starting a fresh instance...'; \
		(cd web && npm install && npm run build) || exit $$?; \
	done

run\:desktop: web
	cd web && KIWI_CODE_BROWSER_BACKEND=electron npm run run:desktop

headless-test:
	go run ./cmd/headless-client

test:
	go test ./...
	go vet ./...
	cd web && npm install && npm test && npm run build
