FROM golang:1.12.5 AS build

WORKDIR /app

COPY go.* ./

RUN go mod download

COPY . ./

RUN go build

FROM debian:stretch-slim
LABEL maintainer="dev@quroumcontrol.com"

COPY --from=library/docker:latest /usr/local/bin/docker /usr/local/bin/docker

COPY --from=build /app/tupelo-integration-runner /usr/local/bin/tupelo-integration-runner

RUN mkdir -p /src/tupelo
WORKDIR /src/tupelo

ENTRYPOINT ["/usr/local/bin/tupelo-integration-runner"]
CMD ["run"]
