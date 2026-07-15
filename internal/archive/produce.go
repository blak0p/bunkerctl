// Package archive produces the final .bunker backup file: it commits the
// container to a temporary image, saves the image with the requested format,
// writes metadata.json into the staging dir, and compresses the whole staging
// tree into a single zstd-tar archive.
package archive

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/blak0p/bunkerctl/internal/compress"
	"github.com/blak0p/bunkerctl/internal/metadata"
	"github.com/blak0p/bunkerctl/internal/podman"
)

// ErrArchiveFailed is returned when any step of archive production fails.
var ErrArchiveFailed = errors.New("archive failed")

// ProduceOptions configures a Produce call.
type ProduceOptions struct {
	// Format is the podman save format: "docker-archive" or "oci-archive".
	Format string
	// BackupDate is the timestamp encoded in the metadata and temp image tag.
	BackupDate time.Time
	// Version is the bunkerctl release version recorded in the archive
	// metadata. When empty, Metadata.Version falls back to "1" (legacy
	// schema version) for backwards compatibility with PR 4 archives.
	Version string
}

// Producer produces a .bunker archive from a container + staging dir.
type Producer interface {
	Produce(ctx context.Context, runner podman.Runner, container podman.InspectResult, stagingDir, destPath string, opts ProduceOptions) (metadata.Metadata, error)
}

// DefaultProducer orchestrates commit → save → metadata → compress. It depends
// on a compress.Compressor and a metadata.Writer so tests can substitute fakes.
type DefaultProducer struct {
	Compressor compress.Compressor
	MetaWriter metadata.Writer
}

// Compile-time guarantee.
var _ Producer = DefaultProducer{}

// Produce runs the archive-production pipeline:
//  1. Commit the container to a temporary image tag.
//  2. Save the image to a temp file in the staging dir with the requested format.
//  3. Write metadata.json to the staging dir.
//  4. Compress the whole staging dir to destPath.
//  5. Return the Metadata (the temp image cleanup is best-effort and handled by
//     the caller in a later restore/upgrade PR; for now we leave it).
func (p DefaultProducer) Produce(ctx context.Context, runner podman.Runner, container podman.InspectResult, stagingDir, destPath string, opts ProduceOptions) (metadata.Metadata, error) {
	// 1. Commit the container to a temporary image tag.
	tmpImage := fmt.Sprintf("bunkerctl-tmp-%d", opts.BackupDate.Unix())
	if err := runner.Commit(ctx, container.ID, tmpImage); err != nil {
		return metadata.Metadata{}, ErrArchiveFailed
	}

	// 2. Save the image to a temp file in the staging dir.
	imagePath := filepath.Join(stagingDir, "image.tar")
	if err := runner.Save(ctx, tmpImage, opts.Format, imagePath); err != nil {
		return metadata.Metadata{}, ErrArchiveFailed
	}

	// 3. Write metadata.json to the staging dir.
	metaVersion := opts.Version
	if metaVersion == "" {
		metaVersion = "1"
	}
	md := metadata.Metadata{
		ContainerName: container.ID,
		Image:         container.Image,
		CreatedAt:     opts.BackupDate,
		Format:        opts.Format,
		Version:       metaVersion,
	}
	metaPath := filepath.Join(stagingDir, "metadata.json")
	if err := p.MetaWriter.Write(metaPath, md); err != nil {
		return metadata.Metadata{}, ErrArchiveFailed
	}

	// 4. Compress the whole staging dir to destPath.
	if err := p.Compressor.Compress(stagingDir, destPath); err != nil {
		return metadata.Metadata{}, ErrArchiveFailed
	}

	return md, nil
}
