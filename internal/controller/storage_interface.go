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

package controller

import (
	"context"
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/fluxcd/source-controller/api/v1"
)

// StorageInterface defines the methods required for artifact storage.
// This allows for different implementations (filesystem, S3, etc.) to be used
// interchangeably by the reconcilers.
type StorageInterface interface {
	// NewArtifactFor returns a new v1.Artifact.
	NewArtifactFor(kind string, metadata metav1.Object, revision, fileName string) v1.Artifact

	// SetArtifactURL sets the public URL of the given artifact.
	SetArtifactURL(artifact *v1.Artifact)

	// Archive creates a tar.gz archive of the given directory.
	Archive(artifact *v1.Artifact, dir string, filter ArchiveFileFilter) error

	// ArtifactExist returns true if the artifact exists in storage.
	ArtifactExist(artifact v1.Artifact) bool

	// CopyFromPath copies a file from the given path to the artifact path.
	CopyFromPath(artifact *v1.Artifact, path string) error

	// CopyToPath copies the artifact to the given path.
	CopyToPath(artifact *v1.Artifact, subPath, toPath string) error

	// MkdirAll creates the directory for the artifact.
	MkdirAll(artifact v1.Artifact) error

	// Remove removes the artifact from storage.
	Remove(artifact v1.Artifact) error

	// RemoveAll removes all artifacts for the given object.
	RemoveAll(artifact v1.Artifact) (string, error)

	// LocalPath returns the local path of the artifact.
	LocalPath(artifact v1.Artifact) string

	// Lock acquires a lock for the given artifact.
	Lock(artifact v1.Artifact) (unlock func(), err error)

	// GarbageCollect removes artifacts that are no longer needed.
	GarbageCollect(ctx context.Context, artifact v1.Artifact, timeout time.Duration) ([]string, error)

	// VerifyArtifact verifies the integrity of the artifact.
	VerifyArtifact(artifact v1.Artifact) error

	// SetHostname sets the hostname for URL generation.
	SetHostname(URL string) string

	// Symlink creates a symbolic link to the artifact.
	Symlink(artifact v1.Artifact, linkName string) (string, error)

	// Copy copies data from a reader to the artifact.
	Copy(artifact *v1.Artifact, reader io.Reader) error
}
