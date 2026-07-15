// Package config loads the bunkerctl configuration file (TOML) into a typed
// Config struct. The config is optional: a missing file is reported via
// ErrConfigNotFound so callers can fall back to defaults without failing.
package config

import (
	"errors"
	"os"

	"github.com/BurntSushi/toml"
)

// ErrConfigNotFound is returned by FileLoader.Load when the configured config
// file does not exist. Callers may treat this as "use defaults" rather than a
// hard error, so first-time users do not need to create a config file.
var ErrConfigNotFound = errors.New("config not found")

// Config holds the parsed bunkerctl configuration. Currently only the
// preserve-list is modeled; later slices extend this struct.
type Config struct {
	// Preserve is the list of paths declared under the [preserve] section.
	// Each entry is either a literal path or a glob pattern; expansion is done
	// downstream by the preserve package.
	Preserve []string `toml:"preserve"`
}

// Loader reads configuration into a Config.
type Loader interface {
	Load() (Config, error)
}

// FileLoader loads Config from a TOML file on disk.
type FileLoader struct {
	// Path is the absolute or relative path to the config TOML file.
	Path string
}

// Compile-time guarantee that FileLoader satisfies Loader.
var _ Loader = FileLoader{}

// Load reads and parses the TOML file. A missing file returns Config{} and
// ErrConfigNotFound. A malformed file returns a parse error. An empty or
// minimal file returns a Config with a nil/empty Preserve slice.
func (f FileLoader) Load() (Config, error) {
	if _, err := os.Stat(f.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, ErrConfigNotFound
		}
		return Config{}, err
	}
	var cfg Config
	if _, err := toml.DecodeFile(f.Path, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}