package sensugo

import "errors"

type Config struct {
	// Whether Sensu integration is enabled.
	Enabled bool `toml:"enabled" override:"enabled"`
	// The Sensu client host:port address.
	URL string `toml:"url" override:"url"`
	// Sensu Go token
	Token string `toml:"token" override:"token"`
	// Default Sensu Go namespace
	Namespace string `toml:"namespace" override:"namespace"`
	// The sensu handlers to use
	Handlers []string `toml:"handlers" override:"handlers"`
}

func NewConfig() Config {
	return Config{}
}

func (c Config) Validate() error {
	if c.Enabled && c.URL == "" {
		return errors.New("must specify backend URL")
	}
	return nil
}
