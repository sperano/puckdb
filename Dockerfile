FROM golang:1.25-alpine AS build
ARG TARGETARCH
ARG GITHUB_TOKEN
ARG GITHUB_RUN_NUMBER
WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN bash -c "/bin/echo 'machine github.com login ericsperano password ${GITHUB_TOKEN}' > /root/.netrc" \
    && GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/sperano/puckdb/config.BuildNumber=${GITHUB_RUN_NUMBER}" \
    && rm /root/.netrc

FROM scratch
COPY --from=build /src/puckdb /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8787
ENTRYPOINT ["/puckdb"]
