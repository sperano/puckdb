FROM alpine:latest as certs
RUN apk --update add ca-certificates

FROM golang:1.18 AS build
ARG TARGETARCH
ARG GITHUB_TOKEN=${GITHUB_TOKEN}
COPY . /src
WORKDIR /src

RUN bash -c "/bin/echo 'machine github.com login ericsperano password ${GITHUB_TOKEN}' > /root/.netrc" \
    && GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 go build \
    && rm /root/.netrc

FROM scratch
COPY --from=build /src/yfh /
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8787
ENTRYPOINT [ "/yfh" ]
