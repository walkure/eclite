// Package config loads eclite's YAML configuration. The shape matches
// config.yaml-dist/config-def.yaml exactly, unchanged from the Perl
// original — existing deployed config.yaml files must keep working.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	BP35A1     BP35A1     `yaml:"bp35a1"`
	Prometheus Prometheus `yaml:"prometheus"`
}

type BP35A1 struct {
	ID     string `yaml:"id"`
	Pass   string `yaml:"pass"`
	Device string `yaml:"device"`
}

type Prometheus struct {
	// WDT is the watchdog window in minutes: if no watt reading has been
	// received within this long, the process exits so the container
	// supervisor restarts it.
	WDT    int               `yaml:"wdt"`
	HTTP   int               `yaml:"http"`
	Labels map[string]string `yaml:"labels"`
}

// Load reads and parses the YAML config at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &cfg, nil
}
