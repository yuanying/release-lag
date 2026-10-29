package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanying/release-lag/internal/github/githubtest"
)

func writeConfig(t *testing.T, apiURL string) string {
	t.Helper()
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "private-key.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf(`owner: example-owner
exclude: [skipped]
github:
  appId: 1
  installationId: 2
  privateKeyFile: %s
  apiURL: %s
`, keyFile, apiURL)
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestOnceCollectsThroughTheAppAndPrintsTheRepositories(t *testing.T) {
	fake := githubtest.New(t,
		&githubtest.Repo{Owner: "example-owner", Name: "tracked", Files: []string{"renovate.json"}},
		&githubtest.Repo{Owner: "example-owner", Name: "skipped", Files: []string{"renovate.json"}},
	)
	// Every request but the token's must carry the installation token.
	fake.Token = "ghs_installation"
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"--config", writeConfig(t, fake.URL), "--once"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got struct {
		Owner        string `json:"owner"`
		LastSuccess  bool   `json:"lastSuccess"`
		Repositories []struct {
			Name string `json:"name"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, stdout.String())
	}
	if got.Owner != "example-owner" || !got.LastSuccess || len(got.Repositories) != 1 || got.Repositories[0].Name != "tracked" {
		t.Errorf("got %s", stdout.String())
	}
}

func TestOnceFailsWhenTheRefreshFails(t *testing.T) {
	fake := githubtest.New(t)
	fake.Fail("/installation/repositories", 500)
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"--config", writeConfig(t, fake.URL), "--once"}, &stdout, &stderr); code == 0 {
		t.Fatal("exit 0")
	}
}

func TestBadStart(t *testing.T) {
	dir := t.TempDir()
	missingKey := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(missingKey, []byte("owner: o\ngithub: {appId: 1, installationId: 2, privateKeyFile: "+filepath.Join(dir, "absent.pem")+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no config file":  {"--config", filepath.Join(dir, "absent.yaml"), "--once"},
		"no private key":  {"--config", missingKey, "--once"},
		"unknown flag":    {"--no-such-flag"},
		"stray arguments": {"--config", missingKey, "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), args, &stdout, &stderr); code != 2 && code != 1 {
				t.Fatalf("exit %d", code)
			}
			if stderr.Len() == 0 {
				t.Error("nothing on stderr")
			}
		})
	}
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Errorf("printed %q", stdout.String())
	}
}
