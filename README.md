# tupelo-integration-runner
Utility for running client integration tests. The recommended way of running tupelo-integration-runner is via the `quorumcontrol/tupelo-integration-runner` docker image.

### Configuration
Ensure the client repo is set to accept a `TUPELO_RPC_HOST` environment variable in the form of `ip:port`. This information is injected automatically into the docker container for the client tests.

In the client repo, create a `.tupelo-integration.yml` file, as an example:
``` yaml
tupelos:
  tupelo-latest:
    image: quorumcontrol/tupelo:latest
  tupelo-master:
    image: quorumcontrol/tupelo:master

testers:
  js-sdk:
    build: .
    command: ["npx", "mocha", "--exit"]
```

* `tupelos` - a map of containers for tupelo rpc-servers w/ name keys
* `testers` - a map of containers for tester instances w/ name keys

Each container is a map consisting of:

* `build` - where to execute docker build
* `image` - use a docker image instead of building
* `command` - what command to run in the docker container (defaults to `rpc-server -l 3` for tupelo containers)

OR

* `docker-compose: true` - when used by itself in a tupelo config it runs `docker-compose up` in the current
directory to run a tupelo (currently only works in tupelo, not in SDKs).

### Running
Run the `tupelo-integration-runner` docker image with a docker.sock mount and a local app mount:
`docker run -v /var/run/docker.sock:/var/run/docker.sock -v $(pwd):/src quorumcontrol/tupelo-integration-runner`

For each combination of a container in the `tupelos` map and a container in the `testers` map, this script will
automatically spin up the rpc-server then build and run the specified container from the `tester` object.
