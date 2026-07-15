// Package metadata writes and reads the backup metadata (metadata.json) that
// accompanies every .bunker archive: source image, date, detected managers,
// preserve count, format used, and the schema version.
package metadata

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// ErrInvalidMetadata is returned when the metadata file cannot be parsed or is
// structurally invalid.
var ErrInvalidMetadata = errors.New("invalid metadata")

// Metadata is the canonical record embedded in every .bunker archive.
type Metadata struct {
	ContainerName string    `json:"container_name"`
	Image         string    `json:"image"`
	CreatedAt     time.Time `json:"created_at"`
	Managers      []string  `json:"managers"`
	PreserveCount int       `json:"preserve_count"`
	Format        string    `json:"format"`
	Version       string    `json:"version"`
}

// Writer writes Metadata to a JSON file.
type Writer interface {
	Write(path string, m Metadata) error
}

// Reader reads Metadata from a JSON file.
type Reader interface {
	Read(path string) (Metadata, error)
}

// JSONWriter encodes Metadata as indented JSON.
type JSONWriter struct{}

// JSONReader decodes Metadata from JSON.
type JSONReader struct{}

// Compile-time guarantees.
var (
	_ Writer = JSONWriter{}
	_ Reader = JSONReader{}
)

// Write encodes m as indented JSON and writes it to path.
func (JSONWriter) Write(path string, m Metadata) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Read decodes the JSON at path into Metadata. Malformed JSON yields
// ErrInvalidMetadata.
func (JSONReader) Read(path string) (Metadata, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	var m Metadata
	if err := json.Unmarshal(b, &m); err != nil {
		return Metadata{}, ErrInvalidMetadata
	}
	return m, nil
}