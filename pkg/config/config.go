package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	AgentID        string `yaml:"agent_id"`
	Port           string `yaml:"port"`
	RedisAddr      string `yaml:"redis_addr"`
	MaxConcurrency int32  `yaml:"max_concurrency"`
}

func LoadAgentConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config AgentConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
