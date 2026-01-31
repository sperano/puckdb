FROM golang:1.25-alpine AS build
ARG TARGETARCH
ARG GITHUB_RUN_NUMBER
WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/sperano/puckdb/config.BuildNumber=${GITHUB_RUN_NUMBER}"

FROM scratch
COPY --from=build /src/puckdb /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8787
ENTRYPOINT ["/puckdb"]
