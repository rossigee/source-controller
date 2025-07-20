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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
)

func TestFilesystemStorage_NewFilesystemStorage(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(storage).NotTo(BeNil())
	g.Expect(storage.Storage.BasePath).To(Equal(tempDir))
	g.Expect(storage.Storage.Hostname).To(Equal("test.local"))
}

func TestFilesystemStorage_StoreRetrieve(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	ctx := context.Background()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	// Create test artifact
	artifact := &v1.Artifact{
		Path: "test/artifact.tar.gz",
	}
	
	// Test content
	content := []byte("test content")
	reader := bytes.NewReader(content)
	
	// Store artifact
	err = storage.Store(ctx, artifact, reader)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(artifact.Digest).NotTo(BeEmpty())
	g.Expect(artifact.Size).NotTo(BeNil())
	g.Expect(*artifact.Size).To(Equal(int64(len(content))))
	
	// Check if artifact exists
	exists, err := storage.Exists(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(exists).To(BeTrue())
	
	// Retrieve artifact
	retrievedReader, err := storage.Retrieve(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	defer retrievedReader.Close()
	
	retrievedContent, err := io.ReadAll(retrievedReader)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(retrievedContent).To(Equal(content))
}

func TestFilesystemStorage_Delete(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	ctx := context.Background()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	// Create and store test artifact
	artifact := &v1.Artifact{
		Path: "test/artifact.tar.gz",
	}
	content := []byte("test content")
	err = storage.Store(ctx, artifact, bytes.NewReader(content))
	g.Expect(err).NotTo(HaveOccurred())
	
	// Verify it exists
	exists, err := storage.Exists(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(exists).To(BeTrue())
	
	// Delete artifact
	err = storage.Delete(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	
	// Verify it's gone
	exists, err = storage.Exists(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(exists).To(BeFalse())
}

func TestFilesystemStorage_GetURL(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	ctx := context.Background()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	artifact := &v1.Artifact{
		Path: "test/artifact.tar.gz",
	}
	
	url, err := storage.GetURL(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(url).To(Equal("http://test.local/test/artifact.tar.gz"))
}

func TestFilesystemStorage_NewArtifactFor(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	metadata := &metav1.ObjectMeta{
		Name:      "test-repo",
		Namespace: "default",
	}
	
	artifact := storage.NewArtifactFor("GitRepository", metadata, "abc123", "latest.tar.gz")
	g.Expect(artifact.Path).To(Equal("gitrepository/default/test-repo/latest.tar.gz"))
	g.Expect(artifact.Revision).To(Equal("abc123"))
}

func TestFilesystemStorage_Archive(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	ctx := context.Background()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	// Create test directory with files
	sourceDir := filepath.Join(tempDir, "source")
	err = os.MkdirAll(sourceDir, 0755)
	g.Expect(err).NotTo(HaveOccurred())
	
	testFile := filepath.Join(sourceDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	g.Expect(err).NotTo(HaveOccurred())
	
	// Create artifact
	artifact := &v1.Artifact{
		Path: "test/archive.tar.gz",
	}
	
	// Create the artifact directory structure first
	err = storage.Storage.MkdirAll(*artifact)
	g.Expect(err).NotTo(HaveOccurred())
	
	// Archive the directory
	opts := ArchiveOptions{
		SourcePath: sourceDir,
		Filter: func(path string, isDir bool) bool {
			return strings.HasSuffix(path, ".ignore")
		},
	}
	
	err = storage.Archive(ctx, artifact, opts)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(artifact.Digest).NotTo(BeEmpty())
	g.Expect(artifact.Size).NotTo(BeNil())
	g.Expect(*artifact.Size).To(BeNumerically(">", 0))
}

func TestFilesystemStorage_Healthy(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	ctx := context.Background()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	err = storage.Healthy(ctx)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestFilesystemStorage_Lock(t *testing.T) {
	g := NewWithT(t)
	tempDir := t.TempDir()
	ctx := context.Background()
	
	storage, err := NewFilesystemStorage(tempDir, "test.local", time.Minute, 2)
	g.Expect(err).NotTo(HaveOccurred())

	artifact := &v1.Artifact{
		Path: "test/artifact.tar.gz",
	}
	
	// Create the artifact directory structure first
	err = storage.Storage.MkdirAll(*artifact)
	g.Expect(err).NotTo(HaveOccurred())
	
	unlock, err := storage.Lock(ctx, artifact)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(unlock).NotTo(BeNil())
	
	// Unlock
	unlock()
}