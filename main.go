package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	headless := flag.Bool("headless", false, "Run without GUI (required on Linux, optional on Windows)")
	configFlag := flag.String("config", "", "Path to config.json (default: next to executable)")
	flag.Parse()

	if *configFlag != "" {
		overrideConfigPath = *configFlag
	}

	if *headless || !hasGUI() {
		runHeadless()
	} else {
		runGUI()
	}
}

func runHeadless() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config from %s: %v\n", configPath(), err)
		fmt.Fprintf(os.Stderr, "Creating default config...\n")
		cfg = defaultConfig()
		if saveErr := saveConfig(cfg); saveErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to save default config: %v\n", saveErr)
		} else {
			fmt.Fprintf(os.Stderr, "Default config written to %s — edit and restart.\n", configPath())
		}
		os.Exit(1)
	}

	if cfg.effectiveMode() == "client" && cfg.RemoteAddr == "" {
		fmt.Fprintf(os.Stderr, "Error: remote_addr not configured in %s\n", configPath())
		os.Exit(1)
	}
	if cfg.effectiveMode() == "server" && cfg.ForwardAddr == "" {
		fmt.Fprintf(os.Stderr, "Error: forward_addr not configured in %s\n", configPath())
		os.Exit(1)
	}

	relay := NewRelay()
	if err := relay.Start(cfg); err != nil {
		log.Fatalf("Failed to start relay: %v", err)
	}
	logRelay(cfg)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	relay.Stop()
	log.Println("Stopped.")
}
