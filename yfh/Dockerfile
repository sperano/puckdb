# Stage to build autobackup
FROM golang:1.17 AS build
COPY . /src
WORKDIR /src
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build
FROM scratch 
COPY --from=build /src/yfh /
COPY yfh.yaml /.yfh.yaml

ENTRYPOINT [ "/yfh" ]