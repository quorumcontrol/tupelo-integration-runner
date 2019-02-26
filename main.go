package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"
)

var dockerCmd string

func runCmd(name string, arg ...string) (string, error) {
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

func runSingle(tester testerConfig, tupeloContainer string) int {
	containerID, cancel, err := dockerRun(tupeloContainer)
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

	cmd := exec.Command(dockerCmd, append([]string{"run", "--rm", "-e", fmt.Sprintf("TUPELO_RPC_HOST=%v:50051", tupeloIP), tester.Image}, tester.Command...)...)
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

func run(cfg *config) {
	setup()

	if cfg.Tester.Image == "" {
		buildPath, err := filepath.Abs(cfg.Tester.Build)
		if err != nil {
			log.Fatalf("error looking up build path %v", err)
		}

		imageId, err := runCmd(dockerCmd, "build", "-q", buildPath)
		if err != nil {
			log.Fatalf("error building image %v", err)
		}
		cfg.Tester.Image = imageId
	}

	var statusCodes []int

	for _, tupeloContainer := range cfg.TupeloImages {
		fmt.Printf("Running test suite with %v\n", tupeloContainer)
		statusCodes = append(statusCodes, runSingle(cfg.Tester, tupeloContainer))
	}

	for _, code := range statusCodes {
		if code != 0 {
			os.Exit(code)
		}
	}

	os.Exit(0)
}

type testerConfig struct {
	Build   string   `yaml:"build"`
	Image   string   `yaml:"image"`
	Command []string `yaml:"command"`
}

type config struct {
	TupeloImages []string     `yaml:"tupeloImages"`
	Tester       testerConfig `yaml:"tester"`
}

func loadConfig(path string) *config {
	var c = &config{}
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatalf("Error getting config file at %v: %v", path, err)
	}
	err = yaml.Unmarshal(yamlFile, c)
	if err != nil {
		log.Fatalf("Error parsing yaml config file at %v: %v", path, err)
	}
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
