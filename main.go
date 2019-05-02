package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"
)

var dockerCmd string

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

func dockerRm(containerID string) error {
	_, err := runCmd(dockerCmd, "rm", "-fv", containerID)
	if err != nil {
		return err
	}
	return nil
}

func dockerRun(args string) (string, func(), error) {
	cmdArgs := append([]string{"run", "-d"}, strings.Fields(args)...)
	containerID, err := runCmd(dockerCmd, cmdArgs...)

	if err != nil {
		return "", nil, err
	}

	return containerID, func() {
		dockerRm(containerID)
	}, nil
}

func dockerPull(image string) error {
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

func runSingle(tester containerConfig, tupelo containerConfig) int {
	if tupelo.Build == "" {
		pullImage(tupelo.Image)
	}

	if tester.Build == "" {
		pullImage(tester.Image)
	}

	version, err := getVersion(tupelo.Image)
	if err != nil {
		log.Error(err)
		return 1
	}

	containerID, cancel, err := dockerRun(strings.Join(append([]string{tupelo.Image}, tupelo.Command...), " "))
	if err != nil {
		log.Error(err)
		return 1
	}
	defer cancel()

	tupeloIP, err := runCmd(dockerCmd, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID)
	if err != nil {
		log.Error(err)
		return 1
	}

	cmd := exec.Command(dockerCmd, append([]string{
		"run", "--rm",
		"-e", fmt.Sprintf("TUPELO_RPC_HOST=%v:50051", tupeloIP),
		"-e", fmt.Sprintf("TUPELO_VERSION=%v", version),
		tester.Image,
	}, tester.Command...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		log.Errorf("%v errored: %v", strings.Join(cmd.Args, " "), err)
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
		if tupelo.Image == "" {
			imageId := buildImage(tupelo.Build)
			tupelo.Image = imageId
		}

		for _, tester := range cfg.TesterConfigs {
			if tester.Image == "" {
				imageId := buildImage(tester.Build)
				tester.Image = imageId
			}

			fmt.Printf("Running %s test suite with %s tupelo\n", tester, tupelo)
			statusCodes = append(statusCodes, runSingle(tester, tupelo))
		}
	}

	for _, code := range statusCodes {
		if code != 0 {
			os.Exit(code)
		}
	}

	os.Exit(0)
}

type containerConfig struct {
	Name    string   `yaml:"name"`
	Build   string   `yaml:"build"`
	Image   string   `yaml:"image"`
	Command []string `yaml:"command"`
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
	if err == nil {
		for n, cfg := range yamlCfg.TesterConfigs {
			c.TesterConfigs = append(c.TesterConfigs, containerConfig{
				Name:    n,
				Build:   cfg.Build,
				Image:   cfg.Image,
				Command: cfg.Command,
			})
		}

		for n, cfg := range yamlCfg.TupeloConfigs {
			var command []string

			if len(cfg.Command) == 0 {
				command = []string{"rpc-server", "-l", "3"}
			} else {
				command = cfg.Command
			}

			c.TupeloConfigs = append(c.TupeloConfigs, containerConfig{
				Name:    n,
				Build:   cfg.Build,
				Image:   cfg.Image,
				Command: command,
			})
		}

		return c
	}

	var cv1 = &yamlConfigV1{}
	errV1 := yaml.Unmarshal(yamlFile, cv1)
	if errV1 != nil {
		log.Fatalf("Error parsing yaml config file at %v: %v", path, err)
	}

	for _, image := range cv1.TupeloImages {
		c.TupeloConfigs = append(c.TupeloConfigs, containerConfig{Image: image})
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
