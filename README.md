# tupelo-integration-runner
Utility for easily running client integration tests. The recommended way of running tupelo-integration-runner is via the `quorumcontrol/tupelo-integration-runner` docker image.

### Usage
This is basically a convience docker image that launches a full local tupelo. All it requires is a conventionally configured `docker-compose.yml`


``` yaml
version: "3"

services:
  integration:
    image: quorumcontrol/tupelo-integration-runner:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - tupelo-integration:/tupelo-integration
    environment:
      TUPELO_VERSION: latest
      COMMUNITY_VERSION: latest

# Customize below this line
  tester:
    build: .
    entrypoint: ["/tupelo-integration/wait-for-tupelo.sh"]
    command: ["npm", "run", "test"]
    volumes:
      - tupelo-integration:/tupelo-integration
# Customize above this line

networks:
  default:
    driver: bridge
    ipam:
      driver: default
      config:
        - subnet: 172.16.247.0/24

volumes:
  tupelo-integration:
```

3 important pieces to point out:
- integration container that mounts `docker.sock` and `/tupelo-integration` volume
- a default network with `172.16.247.0/24` subnet
- a defined `tupelo-integration` volume

Then simply use docker for tests in a container as normal. Mounting `tupelo-integration:/tupelo-integration` will provide assitance in running against this localnet:
- notary group config at `/tupelo-integration/config/notarygroup.toml`
- `/tupelo-integration/wait-for-tupelo.sh` which will sleep until tupelo is running, then exec the provided command