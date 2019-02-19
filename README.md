# tupelo-integration-runner
Utility for running client integration tests. The recommended way of running tupelo-integration-runner is via the `quorumcontrol/tupelo-integration-runner` docker image.

### Configuration
Ensure the client repo is set to accept a `TUPELO_HOST` environment variable in the form of `ip:port`. This information is injected automatically into the docker container for the client tests.

In the client repo, create a `.tupelo-integration.yml` file, as an example:
``` yaml
tupeloImages:
  - quorumcontrol/tupelo:latest tupelo rpc-server -l 3
  - quorumcontrol/tupelo:master tupelo rpc-server -l 3

tester:
  build: .
  command: ["npx", "mocha", "--exit"]
```

`tupeloImages` - an array of full docker run commands for an tupelo rpc-server
`tester.build` - where to execute docker build
`tester.image` - use a docker image instead of building
`tester.command` - what command to run in the docker container


### Running
Run the `tupelo-integration-runner` docker image with a docker.sock mount and a local app mount:
`docker run -v /var/run/docker.sock:/var/run/docker.sock -v $(pwd):/src quorumcontrol/tupelo-integration-runner`

For each item in the `tupeloImages` array, this script will automatically spin up the rpc-server then build and run the specified container from the `tester` object.