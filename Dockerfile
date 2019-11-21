FROM debian:stretch-slim
LABEL maintainer="dev@quroumcontrol.com"

RUN apt-get update && \
    apt-get install -y jq && \
    apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

COPY --from=library/docker:latest /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker/compose:1.25.0-debian /usr/local/bin/docker-compose /usr/bin/docker-compose

# This creates a volume that can be mounted into test runner containers
# and is use for the internal docker-compose staack to mount config files into
VOLUME /tupelo-integration
COPY ./docker/ /tupelo-integration

WORKDIR /tupelo-integration

ENTRYPOINT [ "/tupelo-integration/run.sh" ]