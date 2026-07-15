package client

import (
	"encoding/json"
	"os"

	"github.com/hackafterdark/phosphor/pkg/config"
)

// NewDefaultConfig returns a default configuration.
func NewDefaultConfig() *config.Config {
	return &config.Config{}
}

// NewConfigFromFile loads a configuration from a file.
func NewConfigFromFile(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
