FROM golang:1.27-alpine AS build
ARG TARGETARCH
ARG VERSION
WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/sperano/puckdb/internal/config.BuildNumber=${VERSION}"
# The image distributes the binary, so it carries the licenses of what it links.
RUN GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 sh scripts/collect-licenses.sh /licenses

FROM scratch
COPY --from=build /src/puckdb /
COPY --from=build /src/LICENSE /src/COPYRIGHT /licenses/puckdb/
COPY --from=build /licenses /licenses
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8787
ENTRYPOINT ["/puckdb"]
