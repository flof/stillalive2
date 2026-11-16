TEMPL_VERSION := v0.3.1118
BIN := bin/stillalive
IMPORT_BIN := bin/import-v1

# Loads .env when present; the binary itself does not parse dotenv files.
ENV_LOAD = set -a; if [ -f .env ]; then . ./.env; fi; set +a

.PHONY: all generate watch build run test vet fmt tidy import-v1 docker clean

all: build

generate:
	go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION) generate ./...

# Regenerates templ code and restarts the server on every change, on its own
# .templ and .go files, and proxies http://localhost:7331 with live reload.
# The proxy target must match ADDR (default :8080).
watch:
	@$(ENV_LOAD); go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION) generate \
		--watch \
		--proxy="http://localhost:8080" \
		--cmd='$(ENV_LOAD); go run ./cmd/stillalive'

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/stillalive
	CGO_ENABLED=0 go build -trimpath -o $(IMPORT_BIN) ./cmd/import-v1

# Loads .env when present; see ENV_LOAD above.
run:
	@$(ENV_LOAD); go run ./cmd/stillalive

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

docker:
	docker build -t stillalive:local .

clean:
	rm -rf bin coverage.out
