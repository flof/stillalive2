TEMPL_VERSION := v0.3.1118
BIN := bin/stillalive
IMPORT_BIN := bin/import-v1

.PHONY: all generate build run test vet fmt tidy import-v1 docker clean

all: build

generate:
	go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION) generate ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/stillalive
	CGO_ENABLED=0 go build -trimpath -o $(IMPORT_BIN) ./cmd/import-v1

# Loads .env when present; the binary itself does not parse dotenv files.
run:
	@set -a; if [ -f .env ]; then . ./.env; fi; set +a; go run ./cmd/stillalive

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
