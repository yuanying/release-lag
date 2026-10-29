// Package config reads the configuration file release-lag starts with.
//
// The shape of the file is a contract with the manifests in fleet-infra,
// which mount it from a ConfigMap (ADR 0004). It is read once at start; a
// change rolls the Pod.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	// DefaultPath is where the Deployment mounts the file.
	DefaultPath = "/etc/release-lag/config.yaml"
	// DefaultPrivateKeyFile is where the Deployment mounts the App's key.
	DefaultPrivateKeyFile = "/var/run/secrets/github-app/private-key.pem"
	// DefaultAPIURL is GitHub's REST API.
	DefaultAPIURL = "https://api.github.com"
	// DefaultInterval is how often the repositories are collected.
	DefaultInterval = time.Hour
)

// Config is the whole file.
type Config struct {
	// Owner is the account whose repositories are tracked.
	Owner string `yaml:"owner"`
	// Interval is the time between two collections.
	Interval time.Duration `yaml:"interval"`
	// Exclude names repositories, without the owner, never to track.
	Exclude []string `yaml:"exclude"`
	GitHub  GitHub   `yaml:"github"`
}

// GitHub is how release-lag reaches GitHub, as a GitHub App installation.
type GitHub struct {
	AppID          int64  `yaml:"appId"`
	InstallationID int64  `yaml:"installationId"`
	PrivateKeyFile string `yaml:"privateKeyFile"`
	APIURL         string `yaml:"apiURL"`
}

// Load reads the file at path, fills in the defaults and checks it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// A misspelt key would otherwise be dropped without a word and its
	// default used instead.
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing the config %s: %w", path, err)
	}
	c.setDefaults(data)
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("the config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) setDefaults(data []byte) {
	// An interval written as 0s is a mistake to report, not a request for
	// the default, so the default only applies when the key is absent.
	if c.Interval == 0 && !hasKey(data, "interval") {
		c.Interval = DefaultInterval
	}
	if c.GitHub.PrivateKeyFile == "" {
		c.GitHub.PrivateKeyFile = DefaultPrivateKeyFile
	}
	if c.GitHub.APIURL == "" {
		c.GitHub.APIURL = DefaultAPIURL
	}
	c.GitHub.APIURL = strings.TrimRight(c.GitHub.APIURL, "/")
}

func hasKey(data []byte, key string) bool {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func (c *Config) validate() error {
	var errs []error
	if c.Owner == "" {
		errs = append(errs, errors.New("owner is required"))
	}
	if c.Interval <= 0 {
		errs = append(errs, fmt.Errorf("interval must be positive, not %s", c.Interval))
	}
	if c.GitHub.AppID <= 0 {
		errs = append(errs, errors.New("github.appId is required"))
	}
	if c.GitHub.InstallationID <= 0 {
		errs = append(errs, errors.New("github.installationId is required"))
	}
	return errors.Join(errs...)
}
