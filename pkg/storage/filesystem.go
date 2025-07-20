/*
Copyright 2025 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/fluxcd/source-controller/internal/controller"
)

// FilesystemStorage implements the StorageProvider interface using local filesystem.
// This wraps the existing Storage implementation to maintain backwards compatibility.
type FilesystemStorage struct {
	// Embedded legacy storage for compatibility
	*controller.Storage
}

// NewFilesystemStorage creates a new filesystem-based storage provider.
func NewFilesystemStorage(basePath, hostname string, retentionTTL time.Duration, retentionRecords int) (*FilesystemStorage, error) {
	legacyStorage, err := controller.NewStorage(basePath, hostname, retentionTTL, retentionRecords)
	if err != nil {
		return nil, fmt.Errorf("failed to create legacy storage: %w", err)
	}

	return &FilesystemStorage{
		Storage: legacyStorage,
	}, nil
}

// Store writes the artifact content to the filesystem.
func (fs *FilesystemStorage) Store(ctx context.Context, artifact *v1.Artifact, reader io.Reader) error {
	if err := fs.Storage.MkdirAll(*artifact); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}

	// Use the legacy AtomicWriteFile which handles digest calculation
	if err := fs.Storage.AtomicWriteFile(artifact, reader, 0o600); err != nil {
		return fmt.Errorf("failed to write artifact: %w", err)
	}

	return nil
}

// Retrieve returns a reader for the artifact content.
func (fs *FilesystemStorage) Retrieve(ctx context.Context, artifact *v1.Artifact) (io.ReadCloser, error) {
	if !fs.Storage.ArtifactExist(*artifact) {
		return nil, fmt.Errorf("artifact not found: %s", artifact.Path)
	}

	localPath := fs.Storage.LocalPath(*artifact)
	file, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open artifact: %w", err)
	}

	return file, nil
}

// Exists checks if an artifact exists on the filesystem.
func (fs *FilesystemStorage) Exists(ctx context.Context, artifact *v1.Artifact) (bool, error) {
	return fs.Storage.ArtifactExist(*artifact), nil
}

// Delete removes an artifact from the filesystem.
func (fs *FilesystemStorage) Delete(ctx context.Context, artifact *v1.Artifact) error {
	if err := fs.Storage.Remove(*artifact); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove artifact: %w", err)
	}
	return nil
}

// GetURL returns the HTTP URL for the artifact.
func (fs *FilesystemStorage) GetURL(ctx context.Context, artifact *v1.Artifact) (string, error) {
	if artifact.URL == "" {
		fs.Storage.SetArtifactURL(artifact)
	}
	return artifact.URL, nil
}

// List returns artifacts matching the filter criteria.
func (fs *FilesystemStorage) List(ctx context.Context, filter ArtifactFilter) ([]*v1.Artifact, error) {
	var artifacts []*v1.Artifact
	
	basePath := fs.Storage.BasePath
	if filter.Kind != "" {
		basePath = filepath.Join(basePath, filter.Kind)
		if filter.Namespace != "" {
			basePath = filepath.Join(basePath, filter.Namespace)
			if filter.Name != "" {
				basePath = filepath.Join(basePath, filter.Name)
			}
		}
	}

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and lock files
		if info.IsDir() || strings.HasSuffix(path, ".lock") {
			return nil
		}

		// Extract artifact metadata from path
		relPath, err := filepath.Rel(fs.Storage.BasePath, path)
		if err != nil {
			return err
		}

		parts := strings.Split(relPath, string(filepath.Separator))
		if len(parts) < 4 {
			return nil
		}

		artifact := &v1.Artifact{
			Path:           relPath,
			LastUpdateTime: metav1.NewTime(info.ModTime()),
		}
		fs.Storage.SetArtifactURL(artifact)
		
		artifacts = append(artifacts, artifact)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list artifacts: %w", err)
	}

	return artifacts, nil
}

// GarbageCollect removes old artifacts according to the retention policy.
func (fs *FilesystemStorage) GarbageCollect(ctx context.Context, filter ArtifactFilter, policy RetentionPolicy) ([]string, error) {
	// Create a dummy artifact for the GC operation
	artifact := v1.Artifact{
		Path: filepath.Join(filter.Kind, filter.Namespace, filter.Name, "dummy"),
	}

	// Use legacy GC with timeout
	timeout := 5 * time.Minute
	deleted, err := fs.Storage.GarbageCollect(ctx, artifact, timeout)
	if err != nil {
		return nil, fmt.Errorf("garbage collection failed: %w", err)
	}

	return deleted, nil
}

// Lock acquires an exclusive lock for the artifact.
func (fs *FilesystemStorage) Lock(ctx context.Context, artifact *v1.Artifact) (unlock func(), err error) {
	return fs.Storage.Lock(*artifact)
}

// Healthy checks if the storage is accessible.
func (fs *FilesystemStorage) Healthy(ctx context.Context) error {
	// Check if base path is accessible
	if _, err := os.Stat(fs.Storage.BasePath); err != nil {
		return fmt.Errorf("storage path not accessible: %w", err)
	}
	return nil
}

// NewArtifactFor creates a new artifact with proper path and metadata.
func (fs *FilesystemStorage) NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact {
	return fs.Storage.NewArtifactFor(kind, metadata, revision, fileName)
}

// Archive creates a tar.gz archive from the source directory and stores it.
func (fs *FilesystemStorage) Archive(ctx context.Context, artifact *v1.Artifact, opts ArchiveOptions) error {
	// Convert our filter to the legacy filter type
	var legacyFilter controller.ArchiveFileFilter
	if opts.Filter != nil {
		legacyFilter = func(p string, fi os.FileInfo) bool {
			return opts.Filter(p, fi.IsDir())
		}
	}

	if err := fs.Storage.Archive(artifact, opts.SourcePath, legacyFilter); err != nil {
		return fmt.Errorf("failed to archive: %w", err)
	}

	return nil
}

// CopyFromPath copies a file from the filesystem to storage.
func (fs *FilesystemStorage) CopyFromPath(ctx context.Context, artifact *v1.Artifact, path string) error {
	if err := fs.Storage.CopyFromPath(artifact, path); err != nil {
		return fmt.Errorf("failed to copy from path: %w", err)
	}
	return nil
}

// CopyToPath extracts artifact content to the filesystem.
func (fs *FilesystemStorage) CopyToPath(ctx context.Context, artifact *v1.Artifact, subPath, toPath string) error {
	if err := fs.Storage.CopyToPath(artifact, subPath, toPath); err != nil {
		return fmt.Errorf("failed to copy to path: %w", err)
	}
	return nil
}