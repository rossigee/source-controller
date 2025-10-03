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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/fluxcd/source-controller/internal/controller"
)

// mockS3Provider is a mock implementation of StorageProvider for testing
type mockS3Provider struct {
	artifacts map[string][]byte
	archived  map[string]bool
}

func newMockS3Provider() *mockS3Provider {
	return &mockS3Provider{
		artifacts: make(map[string][]byte),
		archived:  make(map[string]bool),
	}
}

func (m *mockS3Provider) Store(ctx context.Context, artifact *v1.Artifact, reader io.Reader) error {
	m.artifacts[artifact.Path] = []byte("mock-content")
	return nil
}

func (m *mockS3Provider) Retrieve(ctx context.Context, artifact *v1.Artifact) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockS3Provider) Exists(ctx context.Context, artifact *v1.Artifact) (bool, error) {
	_, exists := m.artifacts[artifact.Path]
	return exists, nil
}

func (m *mockS3Provider) Delete(ctx context.Context, artifact *v1.Artifact) error {
	delete(m.artifacts, artifact.Path)
	return nil
}

func (m *mockS3Provider) List(ctx context.Context, filter ArtifactFilter) ([]*v1.Artifact, error) {
	return nil, nil
}

func (m *mockS3Provider) GarbageCollect(ctx context.Context, filter ArtifactFilter, policy RetentionPolicy) ([]string, error) {
	return nil, nil
}

func (m *mockS3Provider) Lock(ctx context.Context, artifact *v1.Artifact) (func(), error) {
	return func() {}, nil
}

func (m *mockS3Provider) Healthy(ctx context.Context) error {
	return nil
}

func (m *mockS3Provider) GetURL(ctx context.Context, artifact *v1.Artifact) (string, error) {
	return "http://mock-s3/" + artifact.Path, nil
}

func (m *mockS3Provider) ResolvePseudoSymlink(ctx context.Context, linkPath string) (string, error) {
	return "", nil
}

func (m *mockS3Provider) Archive(ctx context.Context, artifact *v1.Artifact, opts ArchiveOptions) error {
	m.artifacts[artifact.Path] = []byte("archived-content")
	m.archived[artifact.Path] = true
	return nil
}

func (m *mockS3Provider) CopyFromPath(ctx context.Context, artifact *v1.Artifact, path string) error {
	return nil
}

func (m *mockS3Provider) CopyToPath(ctx context.Context, artifact *v1.Artifact, subPath, toPath string) error {
	return nil
}

func (m *mockS3Provider) NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact {
	return v1.Artifact{
		Path:     filepath.Join(kind, metadata.GetNamespace(), metadata.GetName(), fileName),
		Revision: revision,
	}
}

func TestProviderStorage_ArtifactExist(t *testing.T) {
	g := NewWithT(t)

	// Create mock S3 provider
	mockProvider := newMockS3Provider()

	// Create ProviderStorage
	providerStorage := NewProviderStorage(mockProvider, "/tmp/test", "test.local", time.Hour, 10)

	// Test artifact that doesn't exist
	artifact := v1.Artifact{
		Path: "gitrepository/namespace/name/test.tar.gz",
	}

	// Should return false when artifact doesn't exist in S3
	exists := providerStorage.ArtifactExist(artifact)
	g.Expect(exists).To(BeFalse(), "artifact should not exist in S3")

	// Add artifact to mock S3
	mockProvider.artifacts[artifact.Path] = []byte("test-content")

	// Should return true when artifact exists in S3
	exists = providerStorage.ArtifactExist(artifact)
	g.Expect(exists).To(BeTrue(), "artifact should exist in S3")
}

func TestProviderStorage_Archive(t *testing.T) {
	g := NewWithT(t)

	// Create a temporary directory with test files
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	g.Expect(os.WriteFile(testFile, []byte("test content"), 0644)).To(Succeed())

	// Create mock S3 provider
	mockProvider := newMockS3Provider()

	// Create ProviderStorage
	providerStorage := NewProviderStorage(mockProvider, "/tmp/test", "test.local", time.Hour, 10)

	// Create test artifact
	artifact := v1.Artifact{
		Path: "gitrepository/namespace/name/test.tar.gz",
	}

	// Test with a filter to ensure no nil pointer dereference
	testFilter := func(p string, fi os.FileInfo) bool {
		// This would have panicked before the fix if fi was nil
		return !fi.IsDir() && !strings.HasSuffix(p, ".ignore")
	}

	// Archive should store to S3, not filesystem
	err := providerStorage.Archive(&artifact, tmpDir, testFilter)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify artifact was stored in mock S3
	g.Expect(mockProvider.archived[artifact.Path]).To(BeTrue(), "artifact should be archived in S3")
	g.Expect(mockProvider.artifacts[artifact.Path]).ToNot(BeNil(), "artifact content should exist in S3")
}

func TestProviderStorage_MkdirAll(t *testing.T) {
	g := NewWithT(t)

	// Create ProviderStorage
	providerStorage := NewProviderStorage(newMockS3Provider(), "/tmp/test", "test.local", time.Hour, 10)

	// MkdirAll should be a no-op for S3
	artifact := v1.Artifact{
		Path: "gitrepository/namespace/name/test.tar.gz",
	}

	err := providerStorage.MkdirAll(artifact)
	g.Expect(err).ToNot(HaveOccurred(), "MkdirAll should not error for S3 storage")
}

// TestLegacyStorageAdapter_ReturnsCorrectType tests that NewLegacyStorageAdapter
// returns the correct storage type based on the provider
func TestLegacyStorageAdapter_ReturnsCorrectType(t *testing.T) {
	g := NewWithT(t)

	// Test with filesystem provider
	tmpDir := t.TempDir()
	fsProvider, err := NewFilesystemStorage(tmpDir, "test.local", time.Minute, 5)
	g.Expect(err).ToNot(HaveOccurred())

	fsStorage := NewLegacyStorageAdapter(fsProvider, tmpDir, "test.local")
	g.Expect(fsStorage).To(Equal(controller.StorageInterface(fsProvider.Storage)), "filesystem provider should return embedded Storage")

	// Test with S3 provider (mock)
	s3Provider := newMockS3Provider()
	result := NewLegacyStorageAdapter(s3Provider, "/tmp", "test.local")

	// Should return ProviderStorage, not the embedded controller.Storage
	providerStorage, ok := result.(*ProviderStorage)
	g.Expect(ok).To(BeTrue(), "S3 provider should return ProviderStorage wrapper")
	g.Expect(providerStorage).ToNot(BeNil())

	// Verify the ProviderStorage uses our provider
	g.Expect(providerStorage.provider).To(Equal(s3Provider))
}
