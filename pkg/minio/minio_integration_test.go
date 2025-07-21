//go:build integration
// +build integration

/*
Copyright 2022 The Flux authors

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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	corev1 "k8s.io/api/core/v1"
)

// Integration test constants and variables
const (
	testBucket    = "test-bucket"
	objectName    = "test.txt"
	objectContent = "test-content"
	objectEtag    = "b07bba5a280b58791bc78fb9fc414b09"
)

var (
	// testMinioVersion is the version (image tag) of the Minio server image
	// used to test against.
	testMinioVersion = "RELEASE.2025-07-18T21-56-31Z"
	// testMinioRootUser is the root user of the Minio server.
	testMinioRootUser = "fluxcd"
	// testMinioRootPassword is the root password of the Minio server.
	testMinioRootPassword = "passw0rd!"
	// testMinioAddress is the address of the Minio server, it is set
	// by TestMain after booting it.
	testMinioAddress string
	// testMinioClient is the Minio client used to test against, it is set
	// by TestMain after booting the Minio server.
	testMinioClient *minio.Client
	// testServerCert is the TLS certificate of the Minio server.
	testServerCert tls.Certificate
	// testServerKey is the TLS key of the Minio server.
	testServerKey []byte
	// testTLSConfig is the TLS configuration for the Minio client.
	testTLSConfig *tls.Config
)

func TestMain(m *testing.M) {
	// Uses a sensible default on Windows (TCP/HTTP) and Linux/MacOS (socket)
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not connect to docker: %s", err)
	}

	// Load a private key and certificate from a self-signed CA for the Minio server and
	// a client TLS configuration to connect to the Minio server.
	testServerCert, testServerKey, testTLSConfig, err = loadServerCertAndClientTLSConfig()
	if err != nil {
		log.Fatalf("could not load server cert and client TLS config: %s", err)
	}

	// Pull the image, create a container based on it, and run it
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "minio/minio",
		Tag:        testMinioVersion,
		ExposedPorts: []string{
			"9000/tcp",
			"9001/tcp",
		},
		Env: []string{
			"MINIO_ROOT_USER=" + testMinioRootUser,
			"MINIO_ROOT_PASSWORD=" + testMinioRootPassword,
		},
		Cmd: []string{"server", "/data", "--console-address", ":9001"},
	}, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		log.Fatalf("could not start resource: %s", err)
	}

	testMinioAddress = resource.GetHostPort("9000/tcp")

	// exponential backoff-retry, because the application in the container might not be ready to accept connections yet
	pool.MaxWait = 120 * time.Second
	if err = pool.Retry(func() error {
		testMinioClient, err = minio.New(testMinioAddress, &minio.Options{
			Creds:  minio.NewStaticV4(testMinioRootUser, testMinioRootPassword, ""),
			Secure: false,
		})
		if err != nil {
			return err
		}
		if _, err = testMinioClient.ListBuckets(context.Background()); err != nil {
			return err
		}
		return nil
	}); err != nil {
		log.Fatalf("could not connect to docker: %s", err)
	}

	// Setup
	ctx := context.Background()
	if err = testMinioClient.MakeBucket(ctx, testBucket, minio.MakeBucketOptions{}); err != nil {
		log.Fatalf("could not create test bucket: %s", err)
	}
	_, err = testMinioClient.PutObject(ctx, testBucket, objectName, strings.NewReader(objectContent), int64(len([]byte(objectContent))), minio.PutObjectOptions{
		ContentType: "text/plain",
	})
	if err != nil {
		log.Fatalf("could not create test object: %s", err)
	}

	// run tests
	code := m.Run()

	// You can't defer this because os.Exit doesn't care for defer
	if err := pool.Purge(resource); err != nil {
		log.Fatalf("could not purge resource: %s", err)
	}

	os.Exit(code)
}

func TestNewClient(t *testing.T) {
	client, err := NewClient(testMinioAddress, &minio.Options{
		Creds:  minio.NewStaticV4(testMinioRootUser, testMinioRootPassword, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}
	if client == nil {
		t.Fatal("client is nil")
	}
}

func TestBucketExists(t *testing.T) {
	if exists, err := testMinioClient.BucketExists(context.Background(), testBucket); err != nil {
		t.Fatalf("could not check if bucket exists: %s", err)
	} else if !exists {
		t.Fatalf("bucket %s does not exist", testBucket)
	}
}

func TestBucketNotExists(t *testing.T) {
	if exists, err := testMinioClient.BucketExists(context.Background(), "not-exist"); err != nil {
		t.Fatalf("could not check if bucket exists: %s", err)
	} else if exists {
		t.Fatal("bucket should not exist")
	}
}

func TestFGetObject(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "download.txt")
	if err := testMinioClient.FGetObject(context.Background(), testBucket, objectName, tmpFile, minio.GetObjectOptions{}); err != nil {
		t.Fatalf("could not download object: %s", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("could not read downloaded file: %s", err)
	}
	if string(content) != objectContent {
		t.Fatalf("content mismatch: expected %q, got %q", objectContent, string(content))
	}
}

// Include other integration tests that require a real MinIO server...
// TestNewClientAndFGetObjectWithSTSEndpoint, TestNewClientAndFGetObjectWithProxy, etc.
