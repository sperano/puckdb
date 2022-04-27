FROM alpine:latest as certs
RUN apk --update add ca-certificates

FROM golang:1.18 AS build
ARG TARGETARCH
COPY . /src
WORKDIR /src
RUN GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0 go build

FROM scratch
COPY --from=build /src/yfh /
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8787
ENTRYPOINT [ "/yfh" ]
