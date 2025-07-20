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
	"time"
)

// BackendType represents the storage backend type.
type BackendType string

const (
	// BackendFilesystem uses local filesystem storage (legacy).
	BackendFilesystem BackendType = "filesystem"
	// BackendS3 uses AWS S3 or compatible storage.
	BackendS3 BackendType = "s3"
)

// Config holds the configuration for creating a storage provider.
type Config struct {
	// Backend specifies the storage backend type.
	Backend BackendType

	// Common configuration
	Hostname         string
	RetentionTTL     time.Duration
	RetentionRecords int

	// Filesystem backend configuration
	FilesystemPath string

	// S3 backend configuration
	S3Bucket         string
	S3Prefix         string
	S3Region         string
	S3Endpoint       string
	S3ForcePathStyle bool
	S3URLExpiration  time.Duration
}

// NewProvider creates a new storage provider based on the configuration.
func NewProvider(ctx context.Context, cfg Config) (StorageProvider, error) {
	switch cfg.Backend {
	case BackendFilesystem:
		return newFilesystemProvider(cfg)
	case BackendS3:
		return newS3Provider(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown storage backend: %s", cfg.Backend)
	}
}

// newFilesystemProvider creates a filesystem storage provider.
func newFilesystemProvider(cfg Config) (StorageProvider, error) {
	if cfg.FilesystemPath == "" {
		return nil, fmt.Errorf("filesystem path is required")
	}

	return NewFilesystemStorage(
		cfg.FilesystemPath,
		cfg.Hostname,
		cfg.RetentionTTL,
		cfg.RetentionRecords,
	)
}

// newS3Provider creates an S3 storage provider.
func newS3Provider(ctx context.Context, cfg Config) (StorageProvider, error) {
	if cfg.S3Bucket == "" {
		return nil, fmt.Errorf("S3 bucket is required")
	}

	s3Config := S3Config{
		Bucket:         cfg.S3Bucket,
		Prefix:         cfg.S3Prefix,
		Region:         cfg.S3Region,
		Endpoint:       cfg.S3Endpoint,
		Hostname:       cfg.Hostname,
		URLExpiration:  cfg.S3URLExpiration,
		ForcePathStyle: cfg.S3ForcePathStyle,
	}

	return NewS3Storage(ctx, s3Config)
}