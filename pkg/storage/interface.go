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
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
)

// Interface defines the storage operations for artifacts.
// Implementations must be safe for concurrent use.
type Interface interface {
	// Store writes the artifact content from the reader to storage.
	// It calculates the digest and updates the artifact metadata.
	Store(ctx context.Context, artifact *v1.Artifact, reader io.Reader) error

	// Retrieve returns a reader for the artifact content.
	// The caller is responsible for closing the reader.
	Retrieve(ctx context.Context, artifact *v1.Artifact) (io.ReadCloser, error)

	// Exists checks if an artifact exists in storage.
	Exists(ctx context.Context, artifact *v1.Artifact) (bool, error)

	// Delete removes an artifact from storage.
	Delete(ctx context.Context, artifact *v1.Artifact) error

	// GetURL returns the URL for accessing the artifact.
	// For distributed backends, this may return a pre-signed URL.
	GetURL(ctx context.Context, artifact *v1.Artifact) (string, error)

	// List returns artifacts matching the filter criteria.
	List(ctx context.Context, filter ArtifactFilter) ([]*v1.Artifact, error)

	// GarbageCollect removes artifacts according to the retention policy.
	GarbageCollect(ctx context.Context, filter ArtifactFilter, policy RetentionPolicy) ([]string, error)

	// Lock acquires an exclusive lock for the artifact.
	// Returns a function to release the lock.
	Lock(ctx context.Context, artifact *v1.Artifact) (unlock func(), err error)

	// Healthy checks if the storage backend is available.
	Healthy(ctx context.Context) error
}

// ArtifactFilter defines criteria for filtering artifacts.
type ArtifactFilter struct {
	// Kind filters by artifact kind (e.g., "GitRepository").
	Kind string
	// Namespace filters by Kubernetes namespace.
	Namespace string
	// Name filters by resource name.
	Name string
}

// RetentionPolicy defines garbage collection behavior.
type RetentionPolicy struct {
	// TTL is the duration after which artifacts are eligible for deletion.
	TTL time.Duration
	// MaxRecords is the maximum number of artifacts to retain.
	MaxRecords int
}

// ArchiveOptions defines options for creating archives.
type ArchiveOptions struct {
	// SourcePath is the directory to archive.
	SourcePath string
	// Filter determines which files to include in the archive.
	Filter ArchiveFilter
}

// ArchiveFilter is a function that returns true if a path should be excluded.
type ArchiveFilter func(path string, isDir bool) bool

// StorageProvider defines methods for artifact storage that require
// knowledge of the storage implementation details.
type StorageProvider interface {
	Interface

	// NewArtifactFor creates a new artifact with proper path and metadata.
	NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact

	// Archive creates a tar.gz archive from the source directory and stores it.
	Archive(ctx context.Context, artifact *v1.Artifact, opts ArchiveOptions) error

	// CopyFromPath copies a file from the filesystem to storage.
	CopyFromPath(ctx context.Context, artifact *v1.Artifact, path string) error

	// CopyToPath extracts artifact content to the filesystem.
	CopyToPath(ctx context.Context, artifact *v1.Artifact, subPath, toPath string) error
}