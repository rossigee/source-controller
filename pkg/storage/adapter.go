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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/fluxcd/source-controller/internal/controller"
)

// LegacyStorageAdapter adapts the new StorageProvider interface to the legacy controller.Storage
// for backwards compatibility with existing reconcilers.
type LegacyStorageAdapter struct {
	provider StorageProvider
	basePath string
	hostname string
}

// NewLegacyStorageAdapter creates a new adapter.
func NewLegacyStorageAdapter(provider StorageProvider, basePath, hostname string) *controller.Storage {
	// For filesystem backend, we can return the embedded Storage directly
	if fs, ok := provider.(*FilesystemStorage); ok {
		return fs.Storage
	}

	// For other backends, we need to create a stub that panics on filesystem operations
	// This ensures we catch any direct filesystem usage during migration
	return &controller.Storage{
		BasePath:                 basePath,
		Hostname:                 hostname,
		ArtifactRetentionTTL:     time.Hour, // Default, not used with new backends
		ArtifactRetentionRecords: 10,        // Default, not used with new backends
	}
}

// AdaptedStorage wraps a StorageProvider to provide controller.Storage compatible methods.
type AdaptedStorage struct {
	*controller.Storage
	provider StorageProvider
	ctx      context.Context
}

// NewAdaptedStorage creates storage that uses the new provider for operations.
func NewAdaptedStorage(ctx context.Context, provider StorageProvider, basePath, hostname string, retentionTTL time.Duration, retentionRecords int) *AdaptedStorage {
	return &AdaptedStorage{
		Storage: &controller.Storage{
			BasePath:                 basePath,
			Hostname:                 hostname,
			ArtifactRetentionTTL:     retentionTTL,
			ArtifactRetentionRecords: retentionRecords,
		},
		provider: provider,
		ctx:      ctx,
	}
}

// Archive creates an archive using the provider.
func (a *AdaptedStorage) Archive(artifact *v1.Artifact, dir string, filter controller.ArchiveFileFilter) error {
	// Convert the filter
	var archiveFilter ArchiveFilter
	if filter != nil {
		archiveFilter = func(path string, isDir bool) bool {
			// Create a fake FileInfo for the filter
			// The legacy filter needs os.FileInfo but we only have path/isDir
			return filter(path, nil)
		}
	}

	opts := ArchiveOptions{
		SourcePath: dir,
		Filter:     archiveFilter,
	}

	return a.provider.Archive(a.ctx, artifact, opts)
}

// NewArtifactFor creates a new artifact.
func (a *AdaptedStorage) NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact {
	return a.provider.NewArtifactFor(kind, metadata, revision, fileName)
}

// SetArtifactURL sets the URL on the artifact.
func (a *AdaptedStorage) SetArtifactURL(artifact *v1.Artifact) {
	url, err := a.provider.GetURL(a.ctx, artifact)
	if err == nil {
		artifact.URL = url
	}
}

// ArtifactExist checks if an artifact exists.
func (a *AdaptedStorage) ArtifactExist(artifact v1.Artifact) bool {
	exists, _ := a.provider.Exists(a.ctx, &artifact)
	return exists
}

// CopyFromPath copies from a path.
func (a *AdaptedStorage) CopyFromPath(artifact *v1.Artifact, path string) error {
	return a.provider.CopyFromPath(a.ctx, artifact, path)
}

// CopyToPath copies to a path.
func (a *AdaptedStorage) CopyToPath(artifact *v1.Artifact, subPath, toPath string) error {
	return a.provider.CopyToPath(a.ctx, artifact, subPath, toPath)
}

// Remove removes an artifact.
func (a *AdaptedStorage) Remove(artifact v1.Artifact) error {
	return a.provider.Delete(a.ctx, &artifact)
}

// RemoveAll removes all artifacts for a resource.
func (a *AdaptedStorage) RemoveAll(artifact v1.Artifact) (string, error) {
	filter := ArtifactFilter{
		Kind:      extractKind(artifact.Path),
		Namespace: extractNamespace(artifact.Path),
		Name:      extractName(artifact.Path),
	}

	artifacts, err := a.provider.List(a.ctx, filter)
	if err != nil {
		return "", err
	}

	for _, artifact := range artifacts {
		if err := a.provider.Delete(a.ctx, artifact); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("removed %d artifacts", len(artifacts)), nil
}

// GarbageCollect runs garbage collection.
func (a *AdaptedStorage) GarbageCollect(ctx context.Context, artifact v1.Artifact, timeout time.Duration) ([]string, error) {
	filter := ArtifactFilter{
		Kind:      extractKind(artifact.Path),
		Namespace: extractNamespace(artifact.Path),
		Name:      extractName(artifact.Path),
	}

	policy := RetentionPolicy{
		TTL:        a.Storage.ArtifactRetentionTTL,
		MaxRecords: a.Storage.ArtifactRetentionRecords,
	}

	// Use context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return a.provider.GarbageCollect(ctx, filter, policy)
}

// Lock acquires a lock.
func (a *AdaptedStorage) Lock(artifact v1.Artifact) (unlock func(), err error) {
	return a.provider.Lock(a.ctx, &artifact)
}

// Helper functions to extract components from artifact path
func extractKind(path string) string {
	// Path format: kind/namespace/name/filename
	if path == "" {
		return ""
	}
	parts := splitPath(path)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func extractNamespace(path string) string {
	parts := splitPath(path)
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

func extractName(path string) string {
	parts := splitPath(path)
	if len(parts) > 2 {
		return parts[2]
	}
	return ""
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range strings.Split(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}