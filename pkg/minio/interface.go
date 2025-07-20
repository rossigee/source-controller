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
	"io"

	"github.com/minio/minio-go/v7"
)

// Client defines the interface for MinIO/S3 operations
type Client interface {
	// BucketExists checks if a bucket exists
	BucketExists(ctx context.Context, bucketName string) (bool, error)

	// FGetObject downloads an object to a file
	FGetObject(ctx context.Context, bucketName, objectName, localPath string) (string, error)

	// GetObject retrieves an object
	GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error)

	// PutObject uploads an object
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)

	// RemoveObject deletes an object
	RemoveObject(ctx context.Context, bucketName, objectName string, opts minio.RemoveObjectOptions) error

	// ListObjects lists objects in a bucket
	ListObjects(ctx context.Context, bucketName string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo

	// StatObject gets object metadata
	StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
}

// ensure MinioClient implements Client interface
var _ Client = (*MinioClient)(nil)
