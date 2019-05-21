package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

var dockerCmd string
var dockerComposeCmd string

func runCmd(name string, arg ...string) (string, error) {
	log.Tracef("Running command %v", strings.Join(append([]string{name}, arg...), " "))
	cmd := exec.Command(name, arg...)
	out, err := cmd.CombinedOutput()
	log.Trace(string(out))
	if err != nil {
		return "", fmt.Errorf("%v errored: %v", strings.Join(cmd.Args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runForegroundCmd(name string, arg ...string) error {
	log.Tracef("Running command %v", strings.Join(append([]string{name}, arg...), " "))
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func dockerRm(containerID string) error {
	_, err := runCmd(dockerCmd, "rm", "-fv", containerID)
	if err != nil {
		return err
	}
	return nil
}

func dockerRunArgs(cfg *containerConfig, daemon bool) []string {
	cmdArgs := []string{"run"}

	if daemon {
		cmdArgs = append(cmdArgs, "-d")
	} else {
		cmdArgs = append(cmdArgs, "--rm")
	}

	for k, v := range cfg.Env {
		cmdArgs = append(cmdArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	if cfg.Network != "" {
		cmdArgs = append(cmdArgs, "--net", cfg.Network)
	}

	cmdArgs = append(cmdArgs, cfg.Image)
	cmdArgs = append(cmdArgs, cfg.Command...)

	return cmdArgs
}

func dockerRunDaemon(cfg *containerConfig) (string, func(), error) {
	cmdArgs := dockerRunArgs(cfg, true)

	containerID, err := runCmd(dockerCmd, cmdArgs...)
	if err != nil {
		return "", nil, err
	}

	return containerID, func() {
		dockerRm(containerID)
	}, nil
}

func dockerRunForeground(cfg *containerConfig) error {
	cmdArgs := dockerRunArgs(cfg, false)

	fmt.Println("Running docker", cmdArgs)

	return runForegroundCmd(dockerCmd, cmdArgs...)
}

func dockerPull(image string) error {
	fmt.Println("Pulling image", image)
	_, err := runCmd(dockerCmd, "pull", image)
	if err != nil {
		return err
	}
	return nil
}

func getVersion(tupeloImage string) (string, error) {
	cmdArgs := []string{"run", tupeloImage, "version"}
	version, err := runCmd(dockerCmd, cmdArgs...)

	if err == nil {
		versionRegex := regexp.MustCompile(`v(\d+\.\d+\.\d+)`)
		matchedVersion := versionRegex.FindStringSubmatch(version)

		if len(matchedVersion) > 1 {
			return matchedVersion[1], nil
		}
	}

	tupeloTag := strings.Split(tupeloImage, ":")

	if len(tupeloTag) > 1 {
		return tupeloTag[len(tupeloTag)-1], nil
	}

	return "snapshot", nil
}

func containerIP(nameOrID string) (string, error) {
	maxAttempts := 100

	var (
		cIP string
		err error
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		cIP, err = runCmd(dockerCmd, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", nameOrID)
		if err == nil && cIP != "" {
			return cIP, nil
		}
		time.Sleep(5 * time.Second)
	}

	return "", err
}

type discoverHost struct {
	Addresses []string
	Port      string
}

type hostConfig struct {
	Host string
	Port string
}

func firstOpenPort(dh discoverHost, eachAttempt func()) (openPortHost chan *hostConfig, err chan error) {
	const (
		maxAttempts          = 500
		perAttemptTimeout    = 1 * time.Second
		delayBetweenAttempts = 500 * time.Millisecond
	)

	openPortHost = make(chan *hostConfig, 1)
	err = make(chan error, 1)
	stopSearch := make(chan struct{})

	for _, host := range dh.Addresses {
		go func(h string) {
			for attempt := 0; attempt < maxAttempts; attempt++ {
				select {
				case <-stopSearch:
					return
				default:
					eachAttempt()
					hostAndPort := net.JoinHostPort(h, dh.Port)
					conn, _ := net.DialTimeout("tcp", hostAndPort, perAttemptTimeout)
					if conn != nil {
						_ = conn.Close()
						close(stopSearch)
						openPortHost <- &hostConfig{Host: h, Port: dh.Port}
						return
					}
					time.Sleep(delayBetweenAttempts)
				}
			}
			// if we get here we hit maxAttempts and should send a timeout error on the channel
			err <- fmt.Errorf("maximum attempts (%d) with no hosts reachable", maxAttempts)
		}(host)
	}

	return openPortHost, err
}

func waitForOpenPorts(hosts map[string]discoverHost) (map[string]hostConfig, error) {
	result := make(map[string]hostConfig)

	hostSearches := make(map[string]chan *hostConfig)
	searchErrors := make(map[string]chan error)

	progressUpdate := func() { fmt.Print(".") }

	for hostType, dh := range hosts {
		hostSearches[hostType], searchErrors[hostType] = firstOpenPort(dh, progressUpdate)
	}

	for {
		for hostType, search := range hostSearches {
			select {
			case host := <-search:
				result[hostType] = *host
			case err := <-searchErrors[hostType]:
				return nil, err
			default:
			}
		}

		if len(result) == len(hosts) {
			break
		}
	}

	return result, nil
}

func waitForBootstrapAndRPCServers(bootstrapper discoverHost, rpcServer discoverHost) (discoveredBootstrapper *hostConfig,
	discoveredRpcServer *hostConfig, err error) {
	fmt.Println("Waiting for bootstrapper and RPC server to come up")

	waitResult, err := waitForOpenPorts(map[string]discoverHost{
		"bootstrapper": bootstrapper,
		"rpcServer":    rpcServer,
	})

	fmt.Println()

	if err != nil {
		return nil, nil, err
	}

	bootstrapperHost := waitResult["bootstrapper"]
	rpcServerHost := waitResult["rpcServer"]

	return &bootstrapperHost, &rpcServerHost, nil
}

func dockerHostIP() (string, error) {
	dockerHost := os.Getenv("DOCKER_HOST")

	if dockerHost != "" {
		dockerHostURL, err := url.Parse(dockerHost)
		if err != nil {
			return "", fmt.Errorf("error parsing DOCKER_HOST URL: %v", err)
		}

		// NB: This assumes an IP address, not a hostname
		return dockerHostURL.Hostname(), nil
	}

	return "127.0.0.1", nil
}

var runningTupelo = make(map[string]string)

func runSingle(tester *containerConfig, tupelo *containerConfig) int {
	var (
		bootstrapperIP string
		rpcServerIP    string
		err            error
	)

	if len(runningTupelo) == 0 {
		if tupelo.DockerCompose {
			fmt.Println("Starting tupelo docker-compose stack")
			err := runForegroundCmd(dockerComposeCmd, "up", "-d", "--build", "--force-recreate")
			if err != nil {
				log.Errorf("error running 'docker-compose up': %v", err)
				return 1
			}
			tupelo.StopFunc = func() {
				fmt.Println("Stopping tupelo docker-compose stack")
				_, err := runCmd(dockerComposeCmd, "down")
				if err != nil {
					log.Errorf("error stopping docker-compose stack: %v", err)
					return
				}
			}

			bootstrapperIP, err = containerIP("bootstrap")
			if err != nil {
				log.Error(err)
				return 1
			}

			rpcServerIP, err = containerIP("rpc-server")
			if err != nil {
				log.Error(err)
				return 1
			}

			runningTupelo["network"] = "tupelo_default"

			dockerHost, err := dockerHostIP()
			if err != nil {
				log.Error(err)
				return 1
			}

			_, _, err = waitForBootstrapAndRPCServers(
				discoverHost{Addresses: []string{dockerHost, bootstrapperIP}, Port: "34001"},
				discoverHost{Addresses: []string{dockerHost, rpcServerIP}, Port: "50051"},
			)
			if err != nil {
				log.Error(err)
				return 1
			}
		} else {
			if tupelo.Build == "" {
				pullImage(tupelo.Image)
			}

			fmt.Println("Starting tupelo container")
			containerID, cancel, err := dockerRunDaemon(tupelo)
			if err != nil {
				log.Error(err)
				return 1
			}
			tupelo.StopFunc = func() {
				fmt.Println("Stopping tupelo container")
				cancel()
			}

			rpcServerIP, err = containerIP(containerID)
			if err != nil {
				log.Error(err)
				return 1
			}
		}

		runningTupelo["rpcServerIP"] = rpcServerIP
		runningTupelo["bootstrapperIP"] = bootstrapperIP
	}

	version, err := getVersion(tupelo.Image)
	if err != nil {
		log.Error(err)
		return 1
	}

	if tester.Env == nil {
		tester.Env = make(map[string]string)
	}
	tester.Env["TUPELO_RPC_HOST"] = fmt.Sprintf("%s:50051", runningTupelo["rpcServerIP"])
	if runningTupelo["bootstrapperIP"] != "" {
		tester.Env["TUPELO_BOOTSTRAP_NODES"] = fmt.Sprintf("/ip4/%s/tcp/34001/ipfs/16Uiu2HAm3TGSEKEjagcCojSJeaT5rypaeJMKejijvYSnAjviWwV5", runningTupelo["bootstrapperIP"])
	}
	tester.Env["TUPELO_VERSION"] = version
	if runningTupelo["network"] != "" {
		tester.Network = runningTupelo["network"]
	}

	if tester.Build == "" {
		pullImage(tester.Image)
	}

	err = dockerRunForeground(tester)

	if err != nil {
		log.Errorf("%+v errored: %v", tester, err)
		return 1
	}
	return 0
}

func setup() {
	var err error
	dockerCmd, err = exec.LookPath("docker")
	if err != nil {
		log.Errorf("Could not find docker command: %v", err)
		os.Exit(1)
	}

	dockerComposeCmd, err = exec.LookPath("docker-compose")
	if err != nil {
		log.Warnf("Could not find docker-compose command: %v", err)
		log.Warn("docker-compose builds will not work")
	}

	cmd := exec.Command(dockerCmd, "info")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Errorf("docker info returned error: %v\n%v", err, string(out))
		os.Exit(1)
	}
}

func pullImage(image string) {
	err := dockerPull(image)
	if err != nil {
		// not an error so this can work offline when desired
		log.Warnf("Could not pull latest image: %v", err)
	}
}

func buildImage(buildRoot string) string {
	fmt.Printf("Building Docker image from %s\n", buildRoot)

	buildPath, err := filepath.Abs(buildRoot)
	if err != nil {
		log.Fatalf("error looking up build path %v", err)
	}

	imageId, err := runCmd(dockerCmd, "build", "-q", buildPath)
	if err != nil {
		log.Fatalf("error building image %v", err)
	}
	return imageId
}

func run(cfg *config) {
	setup()

	var statusCodes []int

	for _, tupelo := range cfg.TupeloConfigs {
		if tupelo.DockerCompose {
			if tupelo.Image != "" {
				log.Fatalf("Error in %s: DockerCompose and Image are mutually exclusive", tupelo)
			}
		} else if tupelo.Image == "" {
			if tupelo.Build == "" {
				tupelo.Build = "."
			}
			imageId := buildImage(tupelo.Build)
			tupelo.Image = imageId
		}

		for _, tester := range cfg.TesterConfigs {
			if tester.Image == "" {
				if tester.Build == "" {
					tester.Build = "."
				}
				imageId := buildImage(tester.Build)
				tester.Image = imageId
			}

			fmt.Printf("Running %s test suite with %s tupelo\n", tester, tupelo)
			statusCodes = append(statusCodes, runSingle(&tester, &tupelo))
		}

		tupelo.StopFunc()
		runningTupelo = make(map[string]string)
	}

	for _, code := range statusCodes {
		if code != 0 {
			os.Exit(code)
		}
	}

	os.Exit(0)
}

type containerConfig struct {
	Name          string            `yaml:"name"`
	Build         string            `yaml:"build"`
	Image         string            `yaml:"image"`
	Command       []string          `yaml:"command"`
	Env           map[string]string `yaml:"env"`
	DockerCompose bool              `yaml:"docker-compose"`
	Network       string            `yaml:"network"`
	StopFunc      func()
}

func (c containerConfig) String() string {
	if c.Name != "" {
		return c.Name
	}

	if c.Image != "" {
		return c.Image
	}

	return c.Build
}

type yamlConfigV2 struct {
	TupeloConfigs map[string]containerConfig `yaml:"tupelos"`
	TesterConfigs map[string]containerConfig `yaml:"testers"`
}

type yamlConfigV1 struct {
	TupeloImages []string        `yaml:"tupeloImages"`
	Tester       containerConfig `yaml:"tester"`
}

type config struct {
	TupeloConfigs []containerConfig
	TesterConfigs []containerConfig
}

func loadConfig(path string) *config {
	var c = &config{}

	var yamlCfg = &yamlConfigV2{}

	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatalf("Error getting config file at %v: %v", path, err)
	}
	err = yaml.Unmarshal(yamlFile, yamlCfg)
	if err != nil {
		log.Fatalf("Error parsing yaml config file at %v: %v", path, err)
	}

	if len(yamlCfg.TupeloConfigs) > 0 {
		for n, cfg := range yamlCfg.TesterConfigs {
			c.TesterConfigs = append(c.TesterConfigs, containerConfig{
				Name:    n,
				Build:   cfg.Build,
				Image:   cfg.Image,
				Command: cfg.Command,
			})
		}

		for n, cfg := range yamlCfg.TupeloConfigs {
			c.TupeloConfigs = append(c.TupeloConfigs, containerConfig{
				Name:          n,
				Build:         cfg.Build,
				Image:         cfg.Image,
				Command:       cfg.Command,
				DockerCompose: cfg.DockerCompose,
			})
		}

		return c
	}

	var cv1 = &yamlConfigV1{}
	errV1 := yaml.Unmarshal(yamlFile, cv1)
	if errV1 != nil {
		log.Fatalf("Error parsing yaml config file at %v: %v", path, errV1)
	}

	for _, image := range cv1.TupeloImages {
		imageAndCommand := strings.Split(image, " ")
		c.TupeloConfigs = append(c.TupeloConfigs,
			containerConfig{
				Image:   imageAndCommand[0],
				Command: imageAndCommand[1:],
			})
	}

	c.TesterConfigs = []containerConfig{cv1.Tester}

	return c
}

func main() {
	var logLevel string
	var rootCmd = &cobra.Command{
		Use:   "tupelo-integration",
		Short: "A utility for running integration tests in docker",
		Long:  "A utility for running integration tests in docker",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			lvl, err := log.ParseLevel(logLevel)
			if err != nil {
				log.Fatalf("Could not set log level of %v, use one of %v", logLevel, log.AllLevels)
			}
			log.SetLevel(lvl)
			log.SetFormatter(&log.TextFormatter{ForceColors: true})
		},
	}
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "L", "warn", "set log level for integration test suite debugging")

	var configFile string
	var runCmd = &cobra.Command{
		Use:   "run",
		Short: "Run the integration test suite",
		Long:  "Run the integration test suite",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, err := filepath.Abs(configFile)
			if err != nil {
				panic("Error fetching current directory")
			}

			config := loadConfig(configPath)
			run(config)
		},
	}
	runCmd.Flags().StringVarP(&configFile, "config-file", "c", ".tupelo-integration.yml", "Path to tupelo integration runner yaml configuration file")

	rootCmd.AddCommand(runCmd)
	rootCmd.Execute()
}
