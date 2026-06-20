//go:build ignore

package main

import (
	"fmt"
	"log"
	"os"
)

// Config holds the application configuration.
type Config struct {
	Host string
	Port int
	Mode string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	host := os.Getenv("APP_HOST")
	if host == "" {
		host = "localhost"
	}
	port := 8080
	if val := os.Getenv("APP_PORT"); val != "" {
		fmt.Sscanf(val, "%d", &port)
	}
	return &Config{
		Host: host,
		Port: port,
		Mode: "production",
	}, nil
}

// Serve starts the HTTP server.
func Serve(cfg *Config) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("Serving on %s", addr)
	return nil
}

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := Serve(cfg); err != nil {
		log.Fatal(err)
	}
}
