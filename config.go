package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Mode        string `json:"mode"`         // "client" (default) or "server"
	Transport   string `json:"transport"`    // "udp" (default) or "tcp"
	ListenAddr  string `json:"listen_addr"`  // bind address
	ListenPort  int    `json:"listen_port"`  // bind port
	RemoteAddr  string `json:"remote_addr"`  // client: remote endpoint address
	RemotePort  int    `json:"remote_port"`  // client: remote endpoint port
	ForwardAddr string `json:"forward_addr"` // server: local UDP forward target address
	ForwardPort int    `json:"forward_port"` // server: local UDP forward target port
	AutoStart   bool   `json:"auto_start"`   // Windows: launch on boot
}

func (c Config) effectiveMode() string {
	if c.Mode == "server" {
		return "server"
	}
	return "client"
}

func (c Config) effectiveTransport() string {
	if c.Transport == "tcp" {
		return "tcp"
	}
	return "udp"
}

func defaultConfig() Config {
	return Config{
		Mode:        "client",
		Transport:   "udp",
		ListenAddr:  "0.0.0.0",
		ListenPort:  51820,
		RemoteAddr:  "",
		RemotePort:  51820,
		ForwardAddr: "127.0.0.1",
		ForwardPort: 51820,
		AutoStart:   false,
	}
}

var overrideConfigPath string

func configPath() string {
	if overrideConfigPath != "" {
		return overrideConfigPath
	}
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "config.json")
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return defaultConfig(), err
	}
	cfg := defaultConfig() // start with defaults so missing fields get sane values
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), err
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}
