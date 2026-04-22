package config

import (
	"os"
	"path/filepath"

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

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Resolve TLS cert paths relative to the config file so relative paths
	// in agent.yaml work correctly regardless of the process working directory.
	if cfg.TLS != nil && cfg.TLS.Enabled {
		base := filepath.Dir(path)
		cfg.TLS.CACert = resolveRelative(base, cfg.TLS.CACert)
		cfg.TLS.CertFile = resolveRelative(base, cfg.TLS.CertFile)
		cfg.TLS.KeyFile = resolveRelative(base, cfg.TLS.KeyFile)
	}

	return &cfg, nil
}

// resolveRelative returns path as-is if it is already absolute, otherwise
// joins it with base.
func resolveRelative(base, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}
