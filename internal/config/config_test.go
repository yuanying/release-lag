package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFull(t *testing.T) {
	path := write(t, `
owner: example-owner
interval: 15m
exclude: [scratch, playground]
github:
  appId: 12345
  installationId: 678
  privateKeyFile: /tmp/key.pem
  apiURL: http://127.0.0.1:9999/
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner != "example-owner" {
		t.Errorf("Owner = %q", c.Owner)
	}
	if c.Interval != 15*time.Minute {
		t.Errorf("Interval = %v", c.Interval)
	}
	if strings.Join(c.Exclude, ",") != "scratch,playground" {
		t.Errorf("Exclude = %v", c.Exclude)
	}
	if c.GitHub.AppID != 12345 || c.GitHub.InstallationID != 678 {
		t.Errorf("ids = %d, %d", c.GitHub.AppID, c.GitHub.InstallationID)
	}
	if c.GitHub.PrivateKeyFile != "/tmp/key.pem" {
		t.Errorf("PrivateKeyFile = %q", c.GitHub.PrivateKeyFile)
	}
	// The trailing slash is dropped so paths can be joined on.
	if c.GitHub.APIURL != "http://127.0.0.1:9999" {
		t.Errorf("APIURL = %q", c.GitHub.APIURL)
	}
}

func TestLoadDefaults(t *testing.T) {
	path := write(t, `
owner: example-owner
github:
  appId: 1
  installationId: 2
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != time.Hour {
		t.Errorf("Interval = %v, want 1h", c.Interval)
	}
	if c.GitHub.PrivateKeyFile != DefaultPrivateKeyFile {
		t.Errorf("PrivateKeyFile = %q", c.GitHub.PrivateKeyFile)
	}
	if c.GitHub.APIURL != DefaultAPIURL {
		t.Errorf("APIURL = %q", c.GitHub.APIURL)
	}
	if len(c.Exclude) != 0 {
		t.Errorf("Exclude = %v", c.Exclude)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"no owner":           "github: {appId: 1, installationId: 2}",
		"no app id":          "owner: o\ngithub: {installationId: 2}",
		"no installation id": "owner: o\ngithub: {appId: 1}",
		"bad interval":       "owner: o\ninterval: soon\ngithub: {appId: 1, installationId: 2}",
		"non-positive":       "owner: o\ninterval: 0s\ngithub: {appId: 1, installationId: 2}",
		"unknown field":      "owner: o\nintervall: 1h\ngithub: {appId: 1, installationId: 2}",
		"unknown github key": "owner: o\ngithub: {appId: 1, installationId: 2, token: x}",
		"not yaml":           "owner: [",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("Load succeeded")
	}
}
