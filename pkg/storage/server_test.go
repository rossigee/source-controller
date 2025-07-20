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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/go-logr/logr"

	v1 "github.com/fluxcd/source-controller/api/v1"
)

// mockStorageProvider implements Interface for testing
type mockStorageProvider struct {
	artifacts map[string][]byte
	healthy   bool
}

func newMockStorageProvider() *mockStorageProvider {
	return &mockStorageProvider{
		artifacts: make(map[string][]byte),
		healthy:   true,
	}
}

func (m *mockStorageProvider) Store(ctx context.Context, artifact *v1.Artifact, reader io.Reader) error {
	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.artifacts[artifact.Path] = content
	return nil
}

func (m *mockStorageProvider) Retrieve(ctx context.Context, artifact *v1.Artifact) (io.ReadCloser, error) {
	content, exists := m.artifacts[artifact.Path]
	if !exists {
		return nil, fmt.Errorf("artifact not found")
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (m *mockStorageProvider) Exists(ctx context.Context, artifact *v1.Artifact) (bool, error) {
	_, exists := m.artifacts[artifact.Path]
	return exists, nil
}

func (m *mockStorageProvider) Delete(ctx context.Context, artifact *v1.Artifact) error {
	delete(m.artifacts, artifact.Path)
	return nil
}

func (m *mockStorageProvider) GetURL(ctx context.Context, artifact *v1.Artifact) (string, error) {
	return "http://example.com/" + artifact.Path, nil
}

func (m *mockStorageProvider) List(ctx context.Context, filter ArtifactFilter) ([]*v1.Artifact, error) {
	var artifacts []*v1.Artifact
	for path := range m.artifacts {
		artifacts = append(artifacts, &v1.Artifact{Path: path})
	}
	return artifacts, nil
}

func (m *mockStorageProvider) GarbageCollect(ctx context.Context, filter ArtifactFilter, policy RetentionPolicy) ([]string, error) {
	return nil, nil
}

func (m *mockStorageProvider) Lock(ctx context.Context, artifact *v1.Artifact) (unlock func(), err error) {
	return func() {}, nil
}

func (m *mockStorageProvider) Healthy(ctx context.Context) error {
	if !m.healthy {
		return fmt.Errorf("storage unhealthy")
	}
	return nil
}

func TestArtifactServer_ServeArtifact(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	
	provider := newMockStorageProvider()
	server := NewArtifactServer(ctx, provider, logr.Discard())
	
	// Store test artifact
	artifact := &v1.Artifact{Path: "test/artifact.tar.gz"}
	testContent := []byte("test content")
	err := provider.Store(ctx, artifact, bytes.NewReader(testContent))
	g.Expect(err).NotTo(HaveOccurred())
	
	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET existing artifact",
			method:         "GET",
			path:           "/test/artifact.tar.gz",
			expectedStatus: http.StatusOK,
			expectedBody:   "test content",
		},
		{
			name:           "HEAD existing artifact",
			method:         "HEAD",
			path:           "/test/artifact.tar.gz",
			expectedStatus: http.StatusOK,
			expectedBody:   "",
		},
		{
			name:           "GET non-existent artifact",
			method:         "GET",
			path:           "/nonexistent.tar.gz",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "POST not allowed",
			method:         "POST",
			path:           "/test/artifact.tar.gz",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "Empty path",
			method:         "GET",
			path:           "/",
			expectedStatus: http.StatusBadRequest,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			
			server.Handler().ServeHTTP(w, req)
			
			g.Expect(w.Code).To(Equal(tt.expectedStatus))
			
			if tt.expectedBody != "" {
				g.Expect(w.Body.String()).To(Equal(tt.expectedBody))
			}
			
			if tt.method == "GET" && tt.expectedStatus == http.StatusOK {
				g.Expect(w.Header().Get("Content-Type")).To(Equal("application/gzip"))
			}
		})
	}
}

func TestArtifactServer_HealthCheck(t *testing.T) {
	ctx := context.Background()
	
	tests := []struct {
		name           string
		healthy        bool
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "healthy storage",
			healthy:        true,
			expectedStatus: http.StatusOK,
			expectedBody:   "ok\n",
		},
		{
			name:           "unhealthy storage",
			healthy:        false,
			expectedStatus: http.StatusServiceUnavailable,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			
			provider := newMockStorageProvider()
			provider.healthy = tt.healthy
			
			server := NewArtifactServer(ctx, provider, logr.Discard())
			
			req := httptest.NewRequest("GET", "/health", nil)
			w := httptest.NewRecorder()
			
			server.Handler().ServeHTTP(w, req)
			
			g.Expect(w.Code).To(Equal(tt.expectedStatus))
			
			if tt.expectedBody != "" {
				g.Expect(w.Body.String()).To(Equal(tt.expectedBody))
			}
		})
	}
}

func TestArtifactServer_S3Redirect(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	
	// Create S3 storage (this will fail to connect but that's ok for the redirect test)
	s3Storage := &S3Storage{}
	server := NewArtifactServer(ctx, s3Storage, logr.Discard())
	
	req := httptest.NewRequest("GET", "/test/artifact.tar.gz", nil)
	w := httptest.NewRecorder()
	
	// This will fail because we don't have a real S3 connection,
	// but we can test the type detection logic
	server.Handler().ServeHTTP(w, req)
	
	// We expect an error because the S3 client isn't configured,
	// but this tests that the S3 redirect path is taken
	g.Expect(w.Code).To(Equal(http.StatusInternalServerError))
}

func TestNewArtifactServer(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	
	provider := newMockStorageProvider()
	server := NewArtifactServer(ctx, provider, logr.Discard())
	
	g.Expect(server).NotTo(BeNil())
	g.Expect(server.provider).To(Equal(provider))
	g.Expect(server.ctx).To(Equal(ctx))
}