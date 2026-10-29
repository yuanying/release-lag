// release-lag shows how far each repository with a Renovate config has
// moved on from its latest release.
//
//	release-lag [--config=/etc/release-lag/config.yaml] [--listen=:8080]
//	release-lag --config=... --once
//
// --once collects a single time, prints what GET /api/repos would answer and
// exits, non-zero when the collection failed. It is how `make live` checks
// the App against the real GitHub.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuanying/release-lag/internal/config"
	"github.com/yuanying/release-lag/internal/github"
	"github.com/yuanying/release-lag/internal/githubapp"
	"github.com/yuanying/release-lag/internal/lag"
	"github.com/yuanying/release-lag/internal/server"
)

// version is set at build time with -X main.version.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("release-lag", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", config.DefaultPath, "the configuration file")
	listen := flags.String("listen", ":8080", "the address to serve on")
	once := flags.Bool("once", false, "collect once, print the result as JSON and exit")
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("cannot start", "error", err)
		return 1
	}
	key, err := os.ReadFile(cfg.GitHub.PrivateKeyFile)
	if err != nil {
		log.Error("cannot read the GitHub App private key", "error", err)
		return 1
	}
	tokens, err := githubapp.NewTokenSource(cfg.GitHub.APIURL, cfg.GitHub.AppID, cfg.GitHub.InstallationID, key)
	if err != nil {
		log.Error("cannot start", "error", err)
		return 1
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: &githubapp.Transport{Source: tokens}}
	refresher := lag.NewRefresher(&lag.Collector{
		GitHub:  github.NewClient(cfg.GitHub.APIURL, httpClient),
		Owner:   cfg.Owner,
		Exclude: cfg.Exclude,
	})
	refresher.Log = log
	srv := &server.Server{Refresher: refresher, Owner: cfg.Owner}

	if *once {
		state := refresher.Refresh(ctx)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(srv.Response(state)); err != nil {
			log.Error("cannot print the result", "error", err)
			return 1
		}
		if !state.LastSuccess {
			return 1
		}
		return 0
	}

	log.Info("starting", "version", version, "listen", *listen, "owner", cfg.Owner, "interval", cfg.Interval.String())
	go refresher.Run(ctx, cfg.Interval)
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- httpServer.ListenAndServe() }()
	select {
	case err := <-errc:
		log.Error("serving failed", "error", err)
		return 1
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutting down", "error", err)
		return 1
	}
	return 0
}
