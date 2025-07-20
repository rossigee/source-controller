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
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	v1 "github.com/fluxcd/source-controller/api/v1"
)

// ArtifactServer provides HTTP access to artifacts stored in any storage backend.
// Unlike the legacy file server, this can serve artifacts from distributed storage
// and can run on all pods (not just the leader).
type ArtifactServer struct {
	provider Interface
	logger   logr.Logger
	ctx      context.Context
}

// NewArtifactServer creates a new artifact server.
func NewArtifactServer(ctx context.Context, provider Interface, logger logr.Logger) *ArtifactServer {
	return &ArtifactServer{
		provider: provider,
		logger:   logger,
		ctx:      ctx,
	}
}

// Handler returns an HTTP handler for serving artifacts.
func (s *ArtifactServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveArtifact)
	mux.HandleFunc("/health", s.healthCheck)
	return mux
}

// serveArtifact handles requests for artifacts.
func (s *ArtifactServer) serveArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract artifact path from URL
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		http.Error(w, "Artifact path required", http.StatusBadRequest)
		return
	}

	// Create artifact reference
	artifact := &v1.Artifact{
		Path: path,
	}

	// Check if artifact exists
	exists, err := s.provider.Exists(s.ctx, artifact)
	if err != nil {
		s.logger.Error(err, "Failed to check artifact existence", "path", path)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}

	// For HEAD requests, just return success
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(http.StatusOK)
		return
	}

	// For S3 backend, we can redirect to pre-signed URL
	if _, ok := s.provider.(*S3Storage); ok {
		url, err := s.provider.GetURL(s.ctx, artifact)
		if err != nil {
			s.logger.Error(err, "Failed to get artifact URL", "path", path)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		// Redirect to pre-signed URL
		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
		return
	}

	// For other backends, stream the content
	reader, err := s.provider.Retrieve(s.ctx, artifact)
	if err != nil {
		s.logger.Error(err, "Failed to retrieve artifact", "path", path)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	// Set headers
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	
	// Stream the content
	if _, err := io.Copy(w, reader); err != nil {
		s.logger.Error(err, "Failed to stream artifact", "path", path)
		// Can't send error response after starting to write body
		return
	}
}

// healthCheck handles health check requests.
func (s *ArtifactServer) healthCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()

	if err := s.provider.Healthy(ctx); err != nil {
		s.logger.Error(err, "Storage health check failed")
		http.Error(w, "Storage unhealthy", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok\n")
}

// ListenAndServe starts the artifact server.
func (s *ArtifactServer) ListenAndServe(addr string) error {
	s.logger.Info("Starting artifact server", "addr", addr)
	server := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
		// Timeouts for production use
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // Longer for large artifacts
		IdleTimeout:  120 * time.Second,
	}
	return server.ListenAndServe()
}