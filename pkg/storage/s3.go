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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
	intdigest "github.com/fluxcd/source-controller/internal/digest"
)

// S3Storage implements the StorageProvider interface using MinIO client for S3-compatible storage.
type S3Storage struct {
	client        *minio.Client
	bucket        string
	prefix        string
	hostname      string
	urlExpiration time.Duration
	
	// Lock management
	locks sync.Map
}

// S3Config holds configuration for S3 storage.
type S3Config struct {
	// Bucket is the S3 bucket name.
	Bucket string
	// Prefix is the key prefix for all artifacts.
	Prefix string
	// Region is the AWS region.
	Region string
	// Endpoint is the custom S3 endpoint (for MinIO, etc).
	Endpoint string
	// Hostname is used for generating artifact URLs.
	Hostname string
	// URLExpiration is the duration for pre-signed URLs.
	URLExpiration time.Duration
	// ForcePathStyle enables path-style URLs (required for MinIO).
	ForcePathStyle bool
}

// NewS3Storage creates a new S3-based storage provider using MinIO client.
func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	// Determine endpoint
	endpoint := cfg.Endpoint
	secure := true
	
	if endpoint == "" {
		// Default to AWS S3
		endpoint = "s3.amazonaws.com"
	} else {
		// Parse endpoint to determine if it's secure
		if u, err := url.Parse(endpoint); err == nil {
			secure = u.Scheme == "https"
			endpoint = u.Host
		}
	}

	// Create MinIO client
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewEnvAWS(),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Check if bucket exists
	exists, err := minioClient.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %s does not exist", cfg.Bucket)
	}

	if cfg.URLExpiration == 0 {
		cfg.URLExpiration = 15 * time.Minute
	}

	return &S3Storage{
		client:        minioClient,
		bucket:        cfg.Bucket,
		prefix:        strings.TrimSuffix(cfg.Prefix, "/"),
		hostname:      cfg.Hostname,
		urlExpiration: cfg.URLExpiration,
	}, nil
}

// Store writes the artifact content to S3.
func (s *S3Storage) Store(ctx context.Context, artifact *v1.Artifact, reader io.Reader) error {
	// Calculate digest while reading
	d := intdigest.Canonical.Digester()
	var buf bytes.Buffer
	sz := &writeCounter{}
	mw := io.MultiWriter(d.Hash(), &buf, sz)
	
	if _, err := io.Copy(mw, reader); err != nil {
		return fmt.Errorf("failed to read content: %w", err)
	}

	key := s.artifactKey(artifact)
	
	// Upload to S3 using MinIO client
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(buf.Bytes()), int64(buf.Len()),
		minio.PutObjectOptions{
			ContentType: "application/gzip",
			UserMetadata: map[string]string{
				"digest":   d.Digest().String(),
				"revision": artifact.Revision,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	// Update artifact metadata
	artifact.Digest = d.Digest().String()
	artifact.LastUpdateTime = metav1.Now()
	artifact.Size = &sz.written

	return nil
}

// Retrieve returns a reader for the artifact content from S3.
func (s *S3Storage) Retrieve(ctx context.Context, artifact *v1.Artifact) (io.ReadCloser, error) {
	key := s.artifactKey(artifact)
	
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}

	return obj, nil
}

// Exists checks if an artifact exists in S3.
func (s *S3Storage) Exists(ctx context.Context, artifact *v1.Artifact) (bool, error) {
	key := s.artifactKey(artifact)
	
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		// Check if it's a not found error
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check object existence: %w", err)
	}

	return true, nil
}

// Delete removes an artifact from S3.
func (s *S3Storage) Delete(ctx context.Context, artifact *v1.Artifact) error {
	key := s.artifactKey(artifact)
	
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete object from S3: %w", err)
	}

	return nil
}

// GetURL returns a pre-signed URL for the artifact.
func (s *S3Storage) GetURL(ctx context.Context, artifact *v1.Artifact) (string, error) {
	key := s.artifactKey(artifact)
	
	// Generate pre-signed URL
	url, err := s.client.PresignedGetObject(ctx, s.bucket, key, s.urlExpiration, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create pre-signed URL: %w", err)
	}

	return url.String(), nil
}

// List returns artifacts matching the filter criteria.
func (s *S3Storage) List(ctx context.Context, filter ArtifactFilter) ([]*v1.Artifact, error) {
	prefix := s.prefix
	if prefix != "" {
		prefix += "/"
	}
	
	// Build prefix based on filter
	if filter.Kind != "" {
		prefix += filter.Kind + "/"
		if filter.Namespace != "" {
			prefix += filter.Namespace + "/"
			if filter.Name != "" {
				prefix += filter.Name + "/"
			}
		}
	}

	var artifacts []*v1.Artifact
	
	// List objects with prefix
	objectCh := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	for object := range objectCh {
		if object.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", object.Err)
		}

		// Skip directories
		if strings.HasSuffix(object.Key, "/") {
			continue
		}

		// Extract path relative to prefix
		path := object.Key
		if s.prefix != "" {
			path = strings.TrimPrefix(path, s.prefix+"/")
		}
		
		artifact := &v1.Artifact{
			Path:           path,
			LastUpdateTime: metav1.NewTime(object.LastModified),
		}
		
		size := object.Size
		artifact.Size = &size

		artifacts = append(artifacts, artifact)
	}

	return artifacts, nil
}

// GarbageCollect removes old artifacts according to the retention policy.
func (s *S3Storage) GarbageCollect(ctx context.Context, filter ArtifactFilter, policy RetentionPolicy) ([]string, error) {
	artifacts, err := s.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Sort by last update time (newest first)
	sortArtifactsByTime(artifacts)

	var toDelete []string
	now := time.Now()

	for i, artifact := range artifacts {
		// Check TTL
		age := now.Sub(artifact.LastUpdateTime.Time)
		if age > policy.TTL {
			toDelete = append(toDelete, artifact.Path)
			continue
		}

		// Check max records (keep the newest N records)
		if i >= policy.MaxRecords {
			toDelete = append(toDelete, artifact.Path)
		}
	}

	// Delete artifacts
	var deleted []string
	for _, path := range toDelete {
		artifact := &v1.Artifact{Path: path}
		if err := s.Delete(ctx, artifact); err != nil {
			// Log error but continue
			continue
		}
		deleted = append(deleted, path)
	}

	return deleted, nil
}

// Lock acquires an exclusive lock for the artifact.
func (s *S3Storage) Lock(ctx context.Context, artifact *v1.Artifact) (unlock func(), err error) {
	key := s.artifactKey(artifact)
	
	// Use in-memory locks for now
	// In production, this should use S3 object locks or DynamoDB
	mu := &sync.Mutex{}
	actual, _ := s.locks.LoadOrStore(key, mu)
	mu = actual.(*sync.Mutex)
	
	mu.Lock()
	
	return func() {
		mu.Unlock()
	}, nil
}

// Healthy checks if S3 is accessible.
func (s *S3Storage) Healthy(ctx context.Context) error {
	// Check bucket accessibility by listing a single object
	objectCh := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.prefix + "/.health",
		MaxKeys:   1,
		Recursive: false,
	})
	
	// Consume at least one item from channel to check for errors
	for object := range objectCh {
		if object.Err != nil {
			return fmt.Errorf("S3 health check failed: %w", object.Err)
		}
		break
	}
	
	return nil
}

// NewArtifactFor creates a new artifact with proper path and metadata.
func (s *S3Storage) NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact {
	path := v1.ArtifactPath(kind, metadata.GetNamespace(), metadata.GetName(), fileName)
	artifact := v1.Artifact{
		Path:     path,
		Revision: revision,
	}
	return artifact
}

// Archive creates a tar.gz archive from the source directory and stores it.
func (s *S3Storage) Archive(ctx context.Context, artifact *v1.Artifact, opts ArchiveOptions) error {
	var buf bytes.Buffer
	
	// Create gzip writer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	
	// Walk the source directory
	err := filepath.Walk(opts.SourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip if filtered
		if opts.Filter != nil {
			relPath, _ := filepath.Rel(opts.SourcePath, path)
			if opts.Filter(relPath, info.IsDir()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip non-regular files
		if !info.Mode().IsRegular() {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, path)
		if err != nil {
			return err
		}

		// Update header name to be relative
		relPath, err := filepath.Rel(opts.SourcePath, path)
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to create archive: %w", err)
	}

	// Close writers
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}

	// Store the archive
	return s.Store(ctx, artifact, bytes.NewReader(buf.Bytes()))
}

// CopyFromPath copies a file from the filesystem to storage.
func (s *S3Storage) CopyFromPath(ctx context.Context, artifact *v1.Artifact, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return s.Store(ctx, artifact, file)
}

// CopyToPath extracts artifact content to the filesystem.
func (s *S3Storage) CopyToPath(ctx context.Context, artifact *v1.Artifact, subPath, toPath string) error {
	// Retrieve artifact
	reader, err := s.Retrieve(ctx, artifact)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Create gzip reader
	gr, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	// Create tar reader
	tr := tar.NewReader(gr)

	// Extract files
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Check if this is the file we want
		if subPath != "" && !strings.HasPrefix(header.Name, subPath) {
			continue
		}

		// Calculate target path
		targetPath := toPath
		if subPath != "" {
			rel, _ := filepath.Rel(subPath, header.Name)
			targetPath = filepath.Join(toPath, rel)
		}

		// Create directory
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		// Create file
		file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		// Copy content
		if _, err := io.Copy(file, tr); err != nil {
			file.Close()
			return fmt.Errorf("failed to write file: %w", err)
		}
		file.Close()

		// If we were looking for a specific file and found it, we're done
		if subPath != "" && header.Name == subPath {
			break
		}
	}

	return nil
}

// artifactKey returns the S3 key for an artifact.
func (s *S3Storage) artifactKey(artifact *v1.Artifact) string {
	if s.prefix != "" {
		return s.prefix + "/" + artifact.Path
	}
	return artifact.Path
}

// writeCounter counts bytes written.
type writeCounter struct {
	written int64
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.written += int64(n)
	return n, nil
}

// sortArtifactsByTime sorts artifacts by LastUpdateTime (newest first).
func sortArtifactsByTime(artifacts []*v1.Artifact) {
	// Simple bubble sort for now
	for i := 0; i < len(artifacts); i++ {
		for j := i + 1; j < len(artifacts); j++ {
			if artifacts[j].LastUpdateTime.After(artifacts[i].LastUpdateTime.Time) {
				artifacts[i], artifacts[j] = artifacts[j], artifacts[i]
			}
		}
	}
}