FROM golang:1.12.4 AS build

WORKDIR /app

COPY go.* ./

RUN go mod download

COPY . ./

RUN go build

FROM debian:stretch-slim
LABEL maintainer="dev@quroumcontrol.com"

ENV DOCKERVERSION=18.06.2-ce

RUN apt-get update && \
    apt-get install -y ca-certificates curl && \
    curl -fsSLO https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKERVERSION}.tgz && \
    tar xzvf docker-${DOCKERVERSION}.tgz --strip 1 -C /usr/local/bin docker/docker && \
    rm docker-${DOCKERVERSION}.tgz && \
    rm -rf /var/lib/apt/lists/*

COPY --from=build /app/tupelo-integration-runner /usr/bin/tupelo-integration-runner

WORKDIR /src

ENTRYPOINT ["/usr/bin/tupelo-integration-runner"]
CMD ["run"]
