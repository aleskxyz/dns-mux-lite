package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	var (
		listenAddr     string
		resolversFile  string
		healthCheck    bool
		healthInterval string
		scanDomain     string
		logLevel       string
	)

	flag.StringVar(&listenAddr, "listen", "0.0.0.0:53", "UDP listen address")
	flag.StringVar(&resolversFile, "resolvers-file", "", "File with resolver list")
	flag.BoolVar(&healthCheck, "health-check", true, "Enable background health checks")
	flag.StringVar(&healthInterval, "health-interval", "1m", "Background health-check interval (e.g. 30s, 1m)")
	flag.StringVar(&scanDomain, "scan-domain", "", "Domain for compatibility tests (default: google.com)")
	flag.StringVar(&logLevel, "log-level", "INFO", "Log level: DEBUG, INFO, WARN, ERROR")

	flag.Parse()

	if resolversFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --resolvers-file is required")
		os.Exit(1)
	}

	var level slog.Level
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN", "WARNING":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if scanDomain == "" {
		scanDomain = "google.com"
	}

	resolvers, err := loadResolversFile(resolversFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading resolvers file %q: %v\n", resolversFile, err)
		os.Exit(1)
	}
	if len(resolvers) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no resolvers loaded from %q\n", resolversFile)
		os.Exit(1)
	}

	slog.Info("Loaded resolvers", "count", len(resolvers), "file", filepath.Clean(resolversFile))

	// Initial compatibility + health probe (tests 0-6)
	slog.Info("Running initial resolver probe", "scan_domain", scanDomain)
	results := scanResolversQuiet(resolvers, scanDomain, 64)
	var working []Resolver
	for _, r := range results {
		if r.Status == "WORKING" {
			working = append(working, r.Resolver)
		}
	}
	if len(working) == 0 {
		slog.Warn("No resolvers passed compatibility probe, keeping all")
		working = resolvers
	}

	pool := NewResolverPool(working)
	slog.Info("Initial pool", "resolvers", len(working))

	// Background health-check loop: only probe resolvers that are currently failed.
	if healthCheck {
		d, err := time.ParseDuration(healthInterval)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid --health-interval %q: %v\n", healthInterval, err)
			os.Exit(1)
		}
		go func() {
			ticker := time.NewTicker(d)
			defer ticker.Stop()
			for range ticker.C {
				failed := pool.FailedResolvers()
				if len(failed) == 0 {
					slog.Debug("Health check: no failed resolvers to probe")
					continue
				}

				slog.Info("Running background health check", "scan_domain", scanDomain, "failed", len(failed))
				results := scanResolversQuiet(failed, scanDomain, 32)

				recovered := 0
				for _, r := range results {
					if r.Status == "WORKING" {
						pool.MarkHealthy(r.Resolver)
						recovered++
					}
				}

				slog.Info("Health check complete", "failed", len(failed), "recovered", recovered)
			}
		}()
	}

	udp := NewUDPProxy(listenAddr, pool)
	go func() {
		if err := udp.Start(); err != nil {
			slog.Error("UDP proxy failed", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("Shutting down dns-mux-lite...")
	udp.Stop()
}

func loadResolversFile(path string) ([]Resolver, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var resolvers []Resolver
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, ok := parseResolver(line)
		if ok {
			resolvers = append(resolvers, r)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return resolvers, nil
}

