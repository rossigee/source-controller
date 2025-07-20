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
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestNewProvider(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "filesystem backend",
			config: Config{
				Backend:        BackendFilesystem,
				FilesystemPath: "/tmp",
				Hostname:       "test.local",
				RetentionTTL:   time.Minute,
				RetentionRecords: 2,
			},
			wantErr: false,
		},
		{
			name: "filesystem backend missing path",
			config: Config{
				Backend:  BackendFilesystem,
				Hostname: "test.local",
			},
			wantErr: true,
		},
		{
			name: "s3 backend missing bucket",
			config: Config{
				Backend:  BackendS3,
				Hostname: "test.local",
			},
			wantErr: true,
		},
		{
			name: "unknown backend",
			config: Config{
				Backend:  "unknown",
				Hostname: "test.local",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			
			provider, err := NewProvider(ctx, tt.config)
			
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(provider).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(provider).NotTo(BeNil())
			}
		})
	}
}

func TestBackendTypes(t *testing.T) {
	g := NewWithT(t)
	
	g.Expect(string(BackendFilesystem)).To(Equal("filesystem"))
	g.Expect(string(BackendS3)).To(Equal("s3"))
}