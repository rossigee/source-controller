/*
Copyright 2024 The Flux authors

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

package minio

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
)

// MockClient is a mock implementation of the Client interface for testing
type MockClient struct {
	// BucketExistsFunc allows customizing BucketExists behavior
	BucketExistsFunc func(ctx context.Context, bucketName string) (bool, error)

	// FGetObjectFunc allows customizing FGetObject behavior
	FGetObjectFunc func(ctx context.Context, bucketName, objectName, localPath string) (string, error)

	// GetObjectFunc allows customizing GetObject behavior
	GetObjectFunc func(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error)

	// PutObjectFunc allows customizing PutObject behavior
	PutObjectFunc func(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)

	// RemoveObjectFunc allows customizing RemoveObject behavior
	RemoveObjectFunc func(ctx context.Context, bucketName, objectName string, opts minio.RemoveObjectOptions) error

	// ListObjectsFunc allows customizing ListObjects behavior
	ListObjectsFunc func(ctx context.Context, bucketName string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo

	// StatObjectFunc allows customizing StatObject behavior
	StatObjectFunc func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)

	// Track method calls for assertions
	Calls map[string]int
}

// NewMockClient creates a new mock client with default implementations
func NewMockClient() *MockClient {
	return &MockClient{
		Calls: make(map[string]int),
	}
}

// BucketExists checks if a bucket exists
func (m *MockClient) BucketExists(ctx context.Context, bucketName string) (bool, error) {
	m.Calls["BucketExists"]++
	if m.BucketExistsFunc != nil {
		return m.BucketExistsFunc(ctx, bucketName)
	}
	// Default: bucket exists
	return true, nil
}

// FGetObject downloads an object to a file
func (m *MockClient) FGetObject(ctx context.Context, bucketName, objectName, localPath string) (string, error) {
	m.Calls["FGetObject"]++
	if m.FGetObjectFunc != nil {
		return m.FGetObjectFunc(ctx, bucketName, objectName, localPath)
	}
	// Default: create empty file
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(localPath, []byte("mock content"), 0644); err != nil {
		return "", err
	}
	return localPath, nil
}

// GetObject retrieves an object
func (m *MockClient) GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error) {
	m.Calls["GetObject"]++
	if m.GetObjectFunc != nil {
		return m.GetObjectFunc(ctx, bucketName, objectName, opts)
	}
	return nil, fmt.Errorf("GetObject not implemented in mock")
}

// PutObject uploads an object
func (m *MockClient) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	m.Calls["PutObject"]++
	if m.PutObjectFunc != nil {
		return m.PutObjectFunc(ctx, bucketName, objectName, reader, objectSize, opts)
	}
	return minio.UploadInfo{
		Bucket: bucketName,
		Key:    objectName,
		Size:   objectSize,
	}, nil
}

// RemoveObject deletes an object
func (m *MockClient) RemoveObject(ctx context.Context, bucketName, objectName string, opts minio.RemoveObjectOptions) error {
	m.Calls["RemoveObject"]++
	if m.RemoveObjectFunc != nil {
		return m.RemoveObjectFunc(ctx, bucketName, objectName, opts)
	}
	return nil
}

// ListObjects lists objects in a bucket
func (m *MockClient) ListObjects(ctx context.Context, bucketName string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	m.Calls["ListObjects"]++
	if m.ListObjectsFunc != nil {
		return m.ListObjectsFunc(ctx, bucketName, opts)
	}
	// Default: return empty channel
	ch := make(chan minio.ObjectInfo)
	close(ch)
	return ch
}

// StatObject gets object metadata
func (m *MockClient) StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	m.Calls["StatObject"]++
	if m.StatObjectFunc != nil {
		return m.StatObjectFunc(ctx, bucketName, objectName, opts)
	}
	return minio.ObjectInfo{
		Key:  objectName,
		Size: 1024,
	}, nil
}
