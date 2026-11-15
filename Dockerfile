# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stillalive ./cmd/stillalive
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/import-v1 ./cmd/import-v1
# Create the mount point with the ownership the non-root runtime user needs.
RUN mkdir -p /out/data && chmod 0777 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stillalive /stillalive
COPY --from=build /out/import-v1 /import-v1
COPY --from=build --chown=65532:65532 /out/data /data

ENV ADDR=:8080 \
    DATABASE_PATH=/data/stillalive.db

EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/stillalive"]
