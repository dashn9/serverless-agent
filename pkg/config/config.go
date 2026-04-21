package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// TLSConfig enables mTLS on the agent gRPC server.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CACert   string `yaml:"ca_cert"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// NetworkConfig holds optional network identity for this node.
// NodePrivateIP is auto-detected from interfaces if omitted.
// NodePublicIP must be set manually (e.g. the cloud provider's public IP).
type NetworkConfig struct {
	NodePrivateIP string `yaml:"node_private_ip,omitempty"`
	NodePublicIP  string `yaml:"node_public_ip,omitempty"`
}

type AgentConfig struct {
	AgentID   string         `yaml:"agent_id"`
	Port      string         `yaml:"port"`
	RedisAddr string         `yaml:"redis_addr"`
	Network   *NetworkConfig `yaml:"network,omitempty"`
	TLS       *TLSConfig     `yaml:"tls,omitempty"`
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
