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
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/fluxcd/source-controller/internal/controller"
)

// PseudoSymlink represents a JSON-based symlink for object storage backends.
type PseudoSymlink struct {
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
}

// Ensure ProviderStorage implements controller.StorageInterface
var _ controller.StorageInterface = (*ProviderStorage)(nil)

// ProviderStorage implements controller.Storage interface by delegating to a StorageProvider.
// This ensures all storage operations use the provider (e.g., S3) instead of filesystem.
type ProviderStorage struct {
	// Core configuration
	BasePath                 string
	Hostname                 string
	ArtifactRetentionTTL     time.Duration
	ArtifactRetentionRecords int

	// The actual storage provider (S3, filesystem, etc.)
	provider StorageProvider
}

// NewProviderStorage creates a new storage that delegates to the given provider.
func NewProviderStorage(provider StorageProvider, basePath, hostname string, retentionTTL time.Duration, retentionRecords int) *ProviderStorage {
	ps := &ProviderStorage{
		BasePath:                 basePath,
		Hostname:                 hostname,
		ArtifactRetentionTTL:     retentionTTL,
		ArtifactRetentionRecords: retentionRecords,
		provider:                 provider,
	}
	return ps
}

// MkdirAll is a no-op for non-filesystem storage backends.
func (s *ProviderStorage) MkdirAll(artifact v1.Artifact) error {
	// S3 and other object stores don't need directory creation
	return nil
}

// ArtifactExist checks if an artifact exists using the provider.
func (s *ProviderStorage) ArtifactExist(artifact v1.Artifact) bool {
	ctx := context.Background()
	exists, _ := s.provider.Exists(ctx, &artifact)
	return exists
}

// minimalFileInfo is a minimal implementation of os.FileInfo for filter conversion
type minimalFileInfo struct {
	isDir bool
}

func (fi *minimalFileInfo) Name() string       { return "" }
func (fi *minimalFileInfo) Size() int64        { return 0 }
func (fi *minimalFileInfo) Mode() os.FileMode  { return 0 }
func (fi *minimalFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *minimalFileInfo) IsDir() bool        { return fi.isDir }
func (fi *minimalFileInfo) Sys() interface{}   { return nil }

// Archive creates an archive using the storage provider.
func (s *ProviderStorage) Archive(artifact *v1.Artifact, dir string, filter controller.ArchiveFileFilter) error {
	// Convert the filter
	var archiveFilter ArchiveFilter
	if filter != nil {
		archiveFilter = func(path string, isDir bool) bool {
			// Create a minimal FileInfo for the filter
			fi := &minimalFileInfo{isDir: isDir}
			return filter(path, fi)
		}
	}

	opts := ArchiveOptions{
		SourcePath: dir,
		Filter:     archiveFilter,
	}

	ctx := context.Background()
	return s.provider.Archive(ctx, artifact, opts)
}

// NewArtifactFor creates a new artifact with proper metadata.
func (s *ProviderStorage) NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact {
	artifact := s.provider.NewArtifactFor(kind, metadata, revision, fileName)
	// Set the URL on the artifact, just like the legacy Storage does
	s.SetArtifactURL(&artifact)
	return artifact
}

// SetArtifactURL sets the URL on the artifact using the artifact server endpoint.
func (s *ProviderStorage) SetArtifactURL(artifact *v1.Artifact) {
	if artifact.Path == "" {
		return
	}
	// Use the artifact server endpoint, not direct S3 URLs
	// This allows the server to handle S3 redirects or serve content directly
	format := "http://%s/%s"
	if strings.HasPrefix(s.Hostname, "http://") || strings.HasPrefix(s.Hostname, "https://") {
		format = "%s/%s"
	}
	artifact.URL = fmt.Sprintf(format, s.Hostname, strings.TrimLeft(artifact.Path, "/"))
}

// CopyFromPath copies from a path to storage.
func (s *ProviderStorage) CopyFromPath(artifact *v1.Artifact, path string) error {
	ctx := context.Background()
	return s.provider.CopyFromPath(ctx, artifact, path)
}

// CopyToPath copies from storage to a path.
func (s *ProviderStorage) CopyToPath(artifact *v1.Artifact, subPath, toPath string) error {
	ctx := context.Background()
	return s.provider.CopyToPath(ctx, artifact, subPath, toPath)
}

// Remove removes an artifact from storage.
func (s *ProviderStorage) Remove(artifact v1.Artifact) error {
	ctx := context.Background()
	return s.provider.Delete(ctx, &artifact)
}

// RemoveAll removes all artifacts for a resource.
func (s *ProviderStorage) RemoveAll(artifact v1.Artifact) (string, error) {
	filter := ArtifactFilter{
		Kind:      extractKind(artifact.Path),
		Namespace: extractNamespace(artifact.Path),
		Name:      extractName(artifact.Path),
	}

	ctx := context.Background()
	artifacts, err := s.provider.List(ctx, filter)
	if err != nil {
		return "", err
	}

	for _, a := range artifacts {
		if err := s.provider.Delete(ctx, a); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("removed %d artifacts", len(artifacts)), nil
}

// GarbageCollect runs garbage collection using the provider.
func (s *ProviderStorage) GarbageCollect(ctx context.Context, artifact v1.Artifact, timeout time.Duration) ([]string, error) {
	filter := ArtifactFilter{
		Kind:      extractKind(artifact.Path),
		Namespace: extractNamespace(artifact.Path),
		Name:      extractName(artifact.Path),
	}

	policy := RetentionPolicy{
		TTL:        s.ArtifactRetentionTTL,
		MaxRecords: s.ArtifactRetentionRecords,
	}

	// Use context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return s.provider.GarbageCollect(ctx, filter, policy)
}

// Lock acquires a lock for the artifact.
func (s *ProviderStorage) Lock(artifact v1.Artifact) (unlock func(), err error) {
	ctx := context.Background()
	return s.provider.Lock(ctx, &artifact)
}

// LocalPath returns the local path of the artifact - for S3 this returns empty string.
func (s *ProviderStorage) LocalPath(artifact v1.Artifact) string {
	// For non-filesystem backends, there is no local path
	if _, ok := s.provider.(*FilesystemStorage); ok {
		return filepath.Join(s.BasePath, artifact.Path)
	}
	return ""
}

// VerifyArtifact verifies the integrity of the artifact.
func (s *ProviderStorage) VerifyArtifact(artifact v1.Artifact) error {
	// Check if artifact exists
	ctx := context.Background()
	exists, err := s.provider.Exists(ctx, &artifact)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("artifact not found: %s", artifact.Path)
	}
	// Additional verification could be added here (checksum, etc.)
	return nil
}

// SetHostname sets the hostname of the given URL string to the current Storage.Hostname and returns the result.
func (s *ProviderStorage) SetHostname(URL string) string {
	u, err := url.Parse(URL)
	if err != nil {
		return ""
	}
	u.Host = s.Hostname
	return u.String()
}

// Symlink creates a symbolic link to the artifact.
// For object storage backends, this creates a JSON "pseudo-symlink" file.
func (s *ProviderStorage) Symlink(artifact v1.Artifact, linkName string) (string, error) {
	// For filesystem storage, use traditional symlinks
	if _, ok := s.provider.(*FilesystemStorage); ok {
		// Traditional filesystem symlink implementation would go here
		return "", fmt.Errorf("filesystem symlink not implemented")
	}

	// For object storage, create a JSON pseudo-symlink
	linkPath := linkName + ".redirect.json"

	// Create the pseudo-symlink data
	pseudoLink := PseudoSymlink{
		Target:    artifact.URL,
		CreatedAt: time.Now(),
	}

	// Marshal to JSON
	linkData, err := json.Marshal(pseudoLink)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pseudo-symlink: %w", err)
	}

	// Create a pseudo-artifact for the symlink file
	linkArtifact := v1.Artifact{
		Path: linkPath,
		URL:  "", // Will be set by SetArtifactURL
	}

	// Store the JSON pseudo-symlink
	ctx := context.Background()
	if err := s.provider.Store(ctx, &linkArtifact, strings.NewReader(string(linkData))); err != nil {
		return "", fmt.Errorf("failed to store pseudo-symlink: %w", err)
	}

	// Set and return the symlink URL
	s.SetArtifactURL(&linkArtifact)
	return linkArtifact.URL, nil
}

// ResolvePseudoSymlink resolves a JSON pseudo-symlink and returns the target URL.
// This method should be called by the artifact server when serving pseudo-symlink files.
func (s *ProviderStorage) ResolvePseudoSymlink(ctx context.Context, linkPath string) (string, error) {
	// Check if this is a pseudo-symlink file
	if !strings.HasSuffix(linkPath, ".redirect.json") {
		return "", fmt.Errorf("not a pseudo-symlink file: %s", linkPath)
	}

	// Create artifact for the symlink file
	linkArtifact := v1.Artifact{
		Path: linkPath,
	}

	// Read the pseudo-symlink data
	reader, err := s.provider.Retrieve(ctx, &linkArtifact)
	if err != nil {
		return "", fmt.Errorf("failed to read pseudo-symlink: %w", err)
	}
	defer reader.Close()

	// Parse the JSON
	var pseudoLink PseudoSymlink
	if err := json.NewDecoder(reader).Decode(&pseudoLink); err != nil {
		return "", fmt.Errorf("failed to parse pseudo-symlink: %w", err)
	}

	return pseudoLink.Target, nil
}

// Copy copies data from a reader to the artifact.
func (s *ProviderStorage) Copy(artifact *v1.Artifact, reader io.Reader) error {
	ctx := context.Background()
	return s.provider.Store(ctx, artifact, reader)
}

// NewLegacyStorageAdapter creates a new adapter.
// For S3 backends, it returns a ProviderStorage that properly delegates all operations.
func NewLegacyStorageAdapter(provider StorageProvider, basePath, hostname string) controller.StorageInterface {
	// For filesystem backend, we can return the embedded Storage directly
	if fs, ok := provider.(*FilesystemStorage); ok {
		return fs.Storage
	}

	// For S3 and other backends, use ProviderStorage which properly delegates to provider
	return NewProviderStorage(provider, basePath, hostname, time.Hour, 10)
}

// AdaptedStorage wraps a StorageProvider to provide controller.Storage compatible methods.
type AdaptedStorage struct {
	*controller.Storage
	provider StorageProvider
}

// NewAdaptedStorage creates storage that uses the new provider for operations.
func NewAdaptedStorage(provider StorageProvider, basePath, hostname string, retentionTTL time.Duration, retentionRecords int) *AdaptedStorage {
	return &AdaptedStorage{
		Storage: &controller.Storage{
			BasePath:                 basePath,
			Hostname:                 hostname,
			ArtifactRetentionTTL:     retentionTTL,
			ArtifactRetentionRecords: retentionRecords,
		},
		provider: provider,
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

	ctx := context.Background()
	return a.provider.Archive(ctx, artifact, opts)
}

// NewArtifactFor creates a new artifact.
func (a *AdaptedStorage) NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact {
	return a.provider.NewArtifactFor(kind, metadata, revision, fileName)
}

// SetArtifactURL sets the URL on the artifact.
func (a *AdaptedStorage) SetArtifactURL(artifact *v1.Artifact) {
	ctx := context.Background()
	url, err := a.provider.GetURL(ctx, artifact)
	if err == nil {
		artifact.URL = url
	}
}

// ArtifactExist checks if an artifact exists.
func (a *AdaptedStorage) ArtifactExist(artifact v1.Artifact) bool {
	ctx := context.Background()
	exists, _ := a.provider.Exists(ctx, &artifact)
	return exists
}

// CopyFromPath copies from a path.
func (a *AdaptedStorage) CopyFromPath(artifact *v1.Artifact, path string) error {
	ctx := context.Background()
	return a.provider.CopyFromPath(ctx, artifact, path)
}

// CopyToPath copies to a path.
func (a *AdaptedStorage) CopyToPath(artifact *v1.Artifact, subPath, toPath string) error {
	ctx := context.Background()
	return a.provider.CopyToPath(ctx, artifact, subPath, toPath)
}

// Remove removes an artifact.
func (a *AdaptedStorage) Remove(artifact v1.Artifact) error {
	ctx := context.Background()
	return a.provider.Delete(ctx, &artifact)
}

// RemoveAll removes all artifacts for a resource.
func (a *AdaptedStorage) RemoveAll(artifact v1.Artifact) (string, error) {
	filter := ArtifactFilter{
		Kind:      extractKind(artifact.Path),
		Namespace: extractNamespace(artifact.Path),
		Name:      extractName(artifact.Path),
	}

	ctx := context.Background()
	artifacts, err := a.provider.List(ctx, filter)
	if err != nil {
		return "", err
	}

	for _, artifact := range artifacts {
		if err := a.provider.Delete(ctx, artifact); err != nil {
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
	ctx := context.Background()
	return a.provider.Lock(ctx, &artifact)
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
