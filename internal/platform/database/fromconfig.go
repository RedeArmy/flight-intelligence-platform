package database

import (
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// FromConfig builds a connection Config from the typed application configuration. The caller supplies the role
// (runtime or migrator) and its password, which comes from the SecretStore, never from configuration.
func FromConfig(p config.Postgres, user string, password secret.Secret, appName string) Config {
	return Config{
		Host:             p.Host,
		Port:             p.Port,
		Name:             p.Name,
		User:             user,
		Password:         password,
		SSLMode:          p.SSLMode,
		AppName:          appName,
		MaxConns:         p.MaxConns,
		MinConns:         p.MinConns,
		ConnectTimeout:   p.ConnectTimeout,
		StatementTimeout: p.StatementTimeout,
		MaxConnLifetime:  p.MaxConnLifetime,
	}
}
