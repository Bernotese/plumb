package agent

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Default paths in the guest.
const (
	DefaultConfigPath = "/etc/plumb-agent/agent.json"
	DefaultTokenFile  = "/sys/class/dmi/id/product_serial" // VM: SMBIOS serial, root only (D-025)
	DefaultStateDir   = "/var/lib/plumb-agent"
	CloudInitFlag     = "/etc/cloud/cloud-init.disabled"
	DefaultFullCheck  = 15 * time.Minute
)

// Config is /etc/plumb-agent/agent.json. The installer's static snippet
// writes it; it holds no secret.
type Config struct {
	// URL of the agent listener, for example "https://192.168.1.10:8444".
	URL string `json:"url"`
	// CA is the PEM of the Plumb CA. The agent trusts only certificates it signed.
	CA string `json:"ca"`
	// TokenFile is where the enrollment token is read (default: SMBIOS serial).
	TokenFile string `json:"token_file,omitempty"`
	// StateDir holds the credential and the desired list (default /var/lib/plumb-agent).
	StateDir string `json:"state_dir,omitempty"`
	// FullCheck is the interval of the full drift check, for example "15m".
	FullCheck Duration `json:"full_check,omitempty"`
}

// Duration is a time.Duration written as a Go duration string in JSON.
type Duration time.Duration

// UnmarshalJSON parses "15m".
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON writes "15m0s".
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// LoadConfig reads and checks the agent configuration.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.check(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// check validates the config and fills the defaults.
func (c *Config) check() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("url must be an https URL, got %q", c.URL)
	}
	if !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CA)) {
		return errors.New("ca holds no PEM certificate")
	}
	if c.TokenFile == "" {
		c.TokenFile = DefaultTokenFile
	}
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir
	}
	if !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) == "/" {
		return fmt.Errorf("state_dir must be an absolute directory other than /, got %q", c.StateDir)
	}
	c.StateDir = filepath.Clean(c.StateDir)
	if c.FullCheck <= 0 {
		c.FullCheck = Duration(DefaultFullCheck)
	}
	return nil
}
