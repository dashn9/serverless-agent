package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// TLSConfig enables mTLS on the agent gRPC server.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CACert   string `yaml:"ca_cert"` // Path to CA certificate (for verifying Flux client cert)
	CertFile string `yaml:"cert"`    // Path to agent certificate
	KeyFile  string `yaml:"key"`     // Path to agent private key
}

type AgentConfig struct {
	AgentID   string     `yaml:"agent_id"`
	Port      string     `yaml:"port"`
	RedisAddr string     `yaml:"redis_addr"`
	TLS       *TLSConfig `yaml:"tls,omitempty"`
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
