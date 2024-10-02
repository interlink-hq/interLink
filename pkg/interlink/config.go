package interlink

import (
	"context"
	"flag"
	"os"

	"github.com/containerd/containerd/log"
	"gopkg.in/yaml.v2"
)

// Config holds the whole configuration
type Config struct {
	InterlinkAddress  string `yaml:"InterlinkAddress"`
	Interlinkport     string `yaml:"InterlinkPort"`
	Sidecarurl        string `yaml:"SidecarURL"`
	Sidecarport       string `yaml:"SidecarPort"`
	ExportPodData     bool   `yaml:"ExportPodData"`
	VerboseLogging    bool   `yaml:"VerboseLogging"`
	ErrorsOnlyLogging bool   `yaml:"ErrorsOnlyLogging"`
	DataRootFolder    string `yaml:"DataRootFolder"`
}

// NewInterLinkConfig returns a variable of type InterLinkConfig, used in many other functions and the first encountered error.
func NewInterLinkConfig() (Config, error) {
	var path string
	verbose := flag.Bool("verbose", false, "Enable or disable Debug level logging")
	errorsOnly := flag.Bool("errorsonly", false, "Prints only errors if enabled")
	InterLinkConfigPath := flag.String("interlinkconfigpath", "", "Path to InterLink config")
	flag.Parse()

	interLinkNewConfig := Config{}

	if *verbose {
		interLinkNewConfig.VerboseLogging = true
		interLinkNewConfig.ErrorsOnlyLogging = false
	} else if *errorsOnly {
		interLinkNewConfig.VerboseLogging = false
		interLinkNewConfig.ErrorsOnlyLogging = true
	}

	if *InterLinkConfigPath != "" {
		path = *InterLinkConfigPath
	} else {
		if os.Getenv("INTERLINKCONFIGPATH") != "" {
			path = os.Getenv("INTERLINKCONFIGPATH")
		} else {
			path = "/etc/interlink/InterLinkConfig.yaml"
		}
	}

	if _, err := os.Stat(path); err != nil {
		log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting INTERLINKCONFIGPATH. Exiting...")
		return Config{}, err
	}

	log.G(context.Background()).Info("Loading InterLink config from " + path)
	yfile, err := os.ReadFile(path)
	if err != nil {
		log.G(context.Background()).Error("Error opening config file, exiting...")
		return Config{}, err
	}

	err = yaml.Unmarshal(yfile, &interLinkNewConfig)
	if err != nil {
		return Config{}, err
	}

	if os.Getenv("INTERLINKURL") != "" {
		interLinkNewConfig.InterlinkAddress = os.Getenv("INTERLINKURL")
	}

	if os.Getenv("SIDECARURL") != "" {
		interLinkNewConfig.Sidecarurl = os.Getenv("SIDECARURL")
	}

	if os.Getenv("INTERLINKPORT") != "" {
		interLinkNewConfig.Interlinkport = os.Getenv("INTERLINKPORT")
	}

	if os.Getenv("SIDECARPORT") != "" {
		interLinkNewConfig.Sidecarport = os.Getenv("SIDECARPORT")
	}

	return interLinkNewConfig, nil
}
