package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config represents the top-level configuration
type Config struct {
	Server ServerConfig `mapstructure:"server"`
}

// ServerConfig represents the server-specific configuration
type ServerConfig struct {
	Addr               string           `mapstructure:"addr"`
	Port               int              `mapstructure:"port"`
	AuthToken          string           `mapstructure:"auth-token"`
	AuthBasic          string           `mapstructure:"auth-basic"`
	AuthHmacSecret     string           `mapstructure:"auth-hmac-secret"`
	MaxConcurrentJobs  int              `mapstructure:"max-concurrent-jobs"`
	DefaultConcurrency int              `mapstructure:"default-concurrency"`
	DefaultIterations  int              `mapstructure:"default-iterations"`
	EnableSync         bool             `mapstructure:"enable-sync"`
	TLS                TLSConfig        `mapstructure:"tls"`
	Jobs               []NamedJobConfig `mapstructure:"jobs"`
}

// NamedJobConfig defines a pre-registered execution plan
type NamedJobConfig struct {
	Name        string            `mapstructure:"name" json:"name"`
	Exec        interface{}       `mapstructure:"exec" json:"exec"`
	Concurrency int               `mapstructure:"concurrency" json:"concurrency,omitempty"`
	Iterations  int               `mapstructure:"iterations" json:"iterations,omitempty"`
	Timeout     string            `mapstructure:"timeout" json:"timeout,omitempty"`
	Rate        string            `mapstructure:"rate" json:"rate,omitempty"`
	Vars        map[string]string `mapstructure:"vars" json:"vars,omitempty"`
	Delay       string            `mapstructure:"delay" json:"delay,omitempty"`
	Jitter      string            `mapstructure:"jitter" json:"jitter,omitempty"`
	RampUp      string            `mapstructure:"rampup" json:"rampup,omitempty"`
	Retry       int               `mapstructure:"retry" json:"retry,omitempty"`
	Label       string            `mapstructure:"label" json:"label,omitempty"`
}

// TLSConfig represents TLS settings
type TLSConfig struct {
	Cert string `mapstructure:"cert"`
	Key  string `mapstructure:"key"`
}

// InitViper initializes a viper instance with coxec defaults
func InitViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("COXEC")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	return v
}

// LoadConfig reads the configuration from the specified path or default locations
func LoadConfig(v *viper.Viper, configPath string) (string, error) {
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("coxec")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			if configPath != "" {
				return "", fmt.Errorf("config file not found: %s", configPath)
			}
			return "", nil // Default config not found is fine
		}
		return "", fmt.Errorf("error reading config file: %w", err)
	}

	// Expand environment variables in all string values
	for _, key := range v.AllKeys() {
		val := v.Get(key)
		if s, ok := val.(string); ok {
			v.Set(key, os.ExpandEnv(s))
		}
	}

	return v.ConfigFileUsed(), nil
}
