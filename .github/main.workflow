workflow "Docker Build & Push" {
  on = "push"
  resolves = [
    "Docker Push Latest Image",
  ]
}

action "Master Only" {
  uses = "actions/bin/filter@master"
  args = "branch master"
}

action "Docker Login" {
  uses = "actions/docker/login@8cdf801b322af5f369e00d85e9cf3a7122f49108"
  needs = ["Master Only"]
  secrets = ["DOCKER_PASSWORD", "DOCKER_USERNAME"]
}

action "Docker Build Image" {
  uses = "actions/docker/cli@8cdf801b322af5f369e00d85e9cf3a7122f49108"
  args = "build -t quorumcontrol/tupelo-integration-runner:latest ."
  needs = ["Docker Login"]
}

action "Docker Push Latest Image" {
  uses = "actions/docker/cli@8cdf801b322af5f369e00d85e9cf3a7122f49108"
  needs = ["Docker Build Image"]
  args = "push quorumcontrol/tupelo-integration-runner:latest"
}