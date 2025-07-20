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
	"os"
	"path/filepath"
	"testing"

	"github.com/minio/minio-go/v7"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

func TestValidateSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid secret with accesskey and secretkey",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "valid-secret"},
				Data: map[string][]byte{
					"accesskey": []byte("test-access"),
					"secretkey": []byte("test-secret"),
				},
			},
			wantErr: false,
		},
		{
			name: "empty secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-secret"},
				Data:       map[string][]byte{},
			},
			wantErr: true,
			errMsg:  "invalid 'empty-secret' secret data: required fields 'accesskey' and 'secretkey'",
		},
		{
			name:    "nil secret",
			secret:  nil,
			wantErr: false,
		},
		{
			name: "secret with only accesskey",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "partial-secret"},
				Data: map[string][]byte{
					"accesskey": []byte("test-access"),
				},
			},
			wantErr: true,
			errMsg:  "invalid 'partial-secret' secret data: required fields 'accesskey' and 'secretkey'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecret(tt.secret)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateSecret() error = nil, wantErr %v", tt.wantErr)
				} else if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("ValidateSecret() error = %v, want %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateSecret() error = %v, wantErr %v", err, tt.wantErr)
				}
			}
		})
	}
}

func TestBucketOperations(t *testing.T) {
	mock := NewMockClient()

	t.Run("BucketExists returns true", func(t *testing.T) {
		mock.BucketExistsFunc = func(ctx context.Context, bucketName string) (bool, error) {
			if bucketName != "test-bucket" {
				t.Errorf("unexpected bucket name: %s", bucketName)
			}
			return true, nil
		}

		exists, err := mock.BucketExists(context.Background(), "test-bucket")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected bucket to exist")
		}
		if mock.Calls["BucketExists"] != 1 {
			t.Errorf("expected 1 call to BucketExists, got %d", mock.Calls["BucketExists"])
		}
	})

	t.Run("BucketExists returns false", func(t *testing.T) {
		mock.BucketExistsFunc = func(ctx context.Context, bucketName string) (bool, error) {
			return false, nil
		}

		exists, err := mock.BucketExists(context.Background(), "non-existent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Error("expected bucket not to exist")
		}
	})

	t.Run("BucketExists returns error", func(t *testing.T) {
		mock.BucketExistsFunc = func(ctx context.Context, bucketName string) (bool, error) {
			return false, fmt.Errorf("network error")
		}

		_, err := mock.BucketExists(context.Background(), "error-bucket")
		if err == nil {
			t.Fatal("expected error but got none")
		}
		if err.Error() != "network error" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestFGetObject(t *testing.T) {
	mock := NewMockClient()
	tmpDir := t.TempDir()

	t.Run("successful download", func(t *testing.T) {
		targetPath := filepath.Join(tmpDir, "downloaded.txt")
		content := []byte("test content")

		mock.FGetObjectFunc = func(ctx context.Context, bucketName, objectName, localPath string) (string, error) {
			if err := os.WriteFile(localPath, content, 0644); err != nil {
				return "", err
			}
			return localPath, nil
		}

		path, err := mock.FGetObject(context.Background(), "bucket", "object.txt", targetPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path != targetPath {
			t.Errorf("returned path = %q, want %q", path, targetPath)
		}

		data, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("file content = %q, want %q", data, content)
		}
	})

	t.Run("download error", func(t *testing.T) {
		mock.FGetObjectFunc = func(ctx context.Context, bucketName, objectName, localPath string) (string, error) {
			return "", fmt.Errorf("object not found")
		}

		_, err := mock.FGetObject(context.Background(), "bucket", "missing.txt", "target.txt")
		if err == nil {
			t.Fatal("expected error but got none")
		}
		if err.Error() != "object not found" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestObjectListing(t *testing.T) {
	mock := NewMockClient()

	t.Run("list objects", func(t *testing.T) {
		objects := []minio.ObjectInfo{
			{Key: "file1.txt", Size: 100},
			{Key: "file2.txt", Size: 200},
		}

		mock.ListObjectsFunc = func(ctx context.Context, bucketName string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
			ch := make(chan minio.ObjectInfo)
			go func() {
				defer close(ch)
				for _, obj := range objects {
					ch <- obj
				}
			}()
			return ch
		}

		ch := mock.ListObjects(context.Background(), "bucket", minio.ListObjectsOptions{})

		var received []minio.ObjectInfo
		for obj := range ch {
			received = append(received, obj)
		}

		if len(received) != len(objects) {
			t.Errorf("expected %d objects, got %d", len(objects), len(received))
		}

		for i, obj := range received {
			if obj.Key != objects[i].Key {
				t.Errorf("object %d: key = %q, want %q", i, obj.Key, objects[i].Key)
			}
			if obj.Size != objects[i].Size {
				t.Errorf("object %d: size = %d, want %d", i, obj.Size, objects[i].Size)
			}
		}
	})
}

func TestValidateSTSProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		sts      *sourcev1.BucketSTSSpec
		wantErr  bool
	}{
		{
			name:     "AWS provider with Amazon bucket",
			provider: sourcev1.BucketProviderAmazon,
			sts:      &sourcev1.BucketSTSSpec{Provider: sourcev1.STSProviderAmazon},
			wantErr:  false,
		},
		{
			name:     "LDAP provider with Generic bucket",
			provider: sourcev1.BucketProviderGeneric,
			sts:      &sourcev1.BucketSTSSpec{Provider: sourcev1.STSProviderLDAP},
			wantErr:  false,
		},
		{
			name:     "invalid provider",
			provider: "invalid",
			sts:      &sourcev1.BucketSTSSpec{Provider: "invalid"},
			wantErr:  true,
		},
		{
			name:     "empty provider",
			provider: "",
			sts:      &sourcev1.BucketSTSSpec{Provider: ""},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSTSProvider(tt.provider, tt.sts)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSTSProvider() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
