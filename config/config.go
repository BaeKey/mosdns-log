package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	LogPath             string `yaml:"log_path"`
	DBRetentionDays     int    `yaml:"db_retention_days"`
	LogMaxSizeMB        int64  `yaml:"log_max_size_mb"`
	LogCheckIntervalMin int    `yaml:"log_check_interval_mins"`
	DBCheckIntervalMin  int    `yaml:"db_check_interval_mins"`
	Port                string `yaml:"port"`
	AppLogPath          string `yaml:"app_log_path"`
	AppLogLevel         string `yaml:"app_log_level"`
}

func LoadConfig(path string) (*Config, error) {
	// Defaults
	cfg := &Config{
		LogPath:             "mosdns.log",
		DBRetentionDays:     7,
		LogMaxSizeMB:        50,
		LogCheckIntervalMin: 60, // Default 1 hour
		DBCheckIntervalMin:  60, // Default 1 hour
		Port:                "8080",
		AppLogPath:          "",     // Default to empty (stdout)
		AppLogLevel:         "INFO", // Default to INFO
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Return defaults if no config file
		}
		return nil, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
