FROM golang:1.26-alpine AS build
ARG TARGETARCH
ARG VERSION
WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 go build -v -x \
    -ldflags "-s -w -X github.com/sperano/puckdb/config.BuildNumber=${VERSION}"

FROM scratch
COPY --from=build /src/puckdb /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8787
ENTRYPOINT ["/puckdb"]
