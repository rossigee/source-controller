# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The source-controller is a Kubernetes operator in the Flux CD GitOps toolkit that specializes in artifact acquisition from external sources (Git repositories, OCI registries, Helm repositories, S3-compatible buckets). It reconciles source objects and makes their artifacts available via an HTTP file server.

## Architecture Evolution - v2.0 Distributed Serving

**v1.x Limitation**: The source-controller had a scalability limitation where only the leader pod could serve artifacts. Non-leader replicas were not ready and couldn't serve HTTP requests, creating a single point of failure. See GitHub issue #837.

**v2.0 Solution**: Introduced distributed artifact serving where ALL pods can serve artifacts and are marked as ready. This enables true horizontal scaling, better load distribution, and improved availability.

## Essential Commands

### Development
```bash
# Run controller locally for debugging
make install    # Install CRDs first
make run        # Run controller (add args: ARGS="--storage-adv-addr=:0 --storage-path=./bin/data")

# Run with different storage backends (v2.0+)
make run ARGS="--storage-backend=filesystem --storage-path=./bin/data"
make run ARGS="--storage-backend=s3 --s3-bucket=test-bucket --s3-endpoint=http://localhost:9000"

# Run tests
make test                                                    # All tests
make test-ctrl                                              # Controller tests only
make test GO_TEST_ARGS="-v -run=TestSpecificTest"         # Specific test
make test GO_TEST_ARGS="-tags integration"                 # Integration tests (requires MinIO/S3)
make test GO_TEST_ARGS="-v -tags integration -run=TestMinio" # Specific integration test

# Fuzz testing
make fuzz-native                                            # Native Go fuzz tests (FUZZ_TIME=1m default)
make fuzz-build                                             # Build fuzzers for oss-fuzz
make fuzz-smoketest                                         # Test that fuzzers work

# Build and verify
make                    # Build manager binary
make verify            # Run all checks (fmt, vet, manifests, tidy)
make docker-build      # Build container image
```

### Code Generation
```bash
make generate          # Generate DeepCopy methods
make manifests        # Generate CRDs and RBAC
make api-docs         # Generate API documentation
```

## Code Architecture

### Controller Structure
- Each source type has its own reconciler in `internal/controller/`:
  - `gitrepository_controller.go` - Git repositories
  - `helmrepository_controller.go` - Helm repositories  
  - `helmchart_controller.go` - Helm charts
  - `bucket_controller.go` - S3-compatible buckets
  - `ocirepository_controller.go` - OCI registries

### Storage System

#### v1.x Storage (Legacy)
- Artifacts stored locally at: `/data/<kind>/<namespace>/<name>/<filename>`
- Only leader pod could serve artifacts
- Storage implementation in `internal/controller/storage.go`

#### v2.0 Storage Architecture
- **Storage Interface**: Abstracted storage operations in `pkg/storage/interface.go`
- **Backend Options**:
  - `filesystem`: Local filesystem (default, backwards compatible)
  - `s3`: AWS S3 or S3-compatible (MinIO, GCS with interop)
- **Distributed Serving**: All pods serve artifacts via HTTP on port 9090
- **Storage Factory**: Backend selection via `pkg/storage/factory.go`
- **Configuration Flags**:
  ```
  --storage-backend        # Storage backend type (filesystem, s3)
  --s3-bucket             # S3 bucket name (required for s3 backend)
  --s3-prefix             # S3 key prefix for artifacts
  --s3-region             # S3 region (default: us-east-1)
  --s3-endpoint           # Custom S3 endpoint (for MinIO, etc.)
  --s3-force-path-style   # Force path-style URLs (for MinIO)
  ```
- Implements garbage collection based on TTL and retention count across all backends

### Key Patterns
1. **Reconciliation**: Standard controller-runtime pattern with Result/error returns
2. **Status Updates**: Use `patchHelper` for atomic status updates
3. **Events**: Record Kubernetes events for important state changes
4. **Conditions**: Use `meta.SetResourceCondition()` for status conditions
5. **Artifacts**: Create tar.gz archives with checksums and metadata

## Testing Approach

1. **Unit Tests**: Mock external dependencies using interfaces
2. **Controller Tests**: Use envtest for Kubernetes API simulation
3. **Storage Tests**: Comprehensive tests for all storage backends in `pkg/storage/*_test.go`
4. **Server Tests**: HTTP server tests with mock storage providers
5. **Fuzz Tests**: Located in `tests/fuzz/` for parsing logic
6. **E2E Tests**: Full integration tests with real clusters

### Storage Package Tests
- `factory_test.go`: Backend selection and configuration validation
- `filesystem_test.go`: Complete filesystem storage backend testing
- `server_test.go`: HTTP artifact server with health checks and error handling
- `pkg/minio/minio_test.go`: Unit tests with mocked S3 client
- `pkg/minio/minio_integration_test.go`: Integration tests with real MinIO server

### Testing with MinIO (v2.0+)
```bash
# Start local MinIO for testing
docker run -d -p 9000:9000 -p 9001:9001 \
  -e MINIO_ROOT_USER=minioadmin \
  -e MINIO_ROOT_PASSWORD=minioadmin \
  --name minio \
  minio/minio server /data --console-address ":9001"

# Run integration tests
export MINIO_ENDPOINT=http://localhost:9000
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin
go test -v ./pkg/minio -tags=integration
```

## Important Implementation Details

### v1.x Behavior
- The file server starts immediately in a goroutine (not gated by leader election)
- But readiness probe only succeeds for leader pod

### v2.0 Changes
- **All pods are ready**: Readiness probe succeeds for all pods, not just leader
- **Health checks**: `/health` endpoint verifies storage backend accessibility
- **Storage abstraction**: Controllers use `Storage` interface, not concrete implementation
- **URL consistency**: Artifact URLs remain the same regardless of backend
- **Atomic operations**: All storage backends ensure atomic writes via temp files
- **Distributed GC**: Garbage collection coordinated across all storage backends

### Common Implementation Details
- Use `digest.Canonical` for consistent artifact checksums
- Authentication credentials are read from Secrets referenced in spec
- Signature verification supports Cosign and Notation
- Helm repository indexes are cached to reduce network calls
- Git operations use go-git library with custom transports for auth

## Common Development Tasks

### Adding a New Source Type
1. Define API types in `api/v1/`
2. Generate manifests: `make manifests api-docs`
3. Create controller in `internal/controller/`
4. Add reconciler to `main.go`
5. Write tests following existing patterns

### Adding a New Storage Backend (v2.0+)
1. Implement `Storage` interface in `pkg/storage/`
2. Add backend type to `factory.go`
3. Add configuration flags in `main.go`
4. Write unit and integration tests
5. Update documentation

### Debugging Locally
Use VSCode launch.json:
```json
{
    "name": "Launch Package",
    "type": "go",
    "request": "launch",
    "mode": "auto",
    "program": "${workspaceFolder}/main.go",
    "args": ["--storage-adv-addr=:0", "--storage-path=${workspaceFolder}/bin/data"]
}
```

For S3 backend debugging:
```json
{
    "name": "Launch with S3",
    "type": "go",
    "request": "launch",
    "mode": "auto",
    "program": "${workspaceFolder}/main.go",
    "args": [
        "--storage-backend=s3",
        "--s3-bucket=flux-artifacts",
        "--s3-endpoint=http://localhost:9000",
        "--s3-force-path-style=true"
    ],
    "env": {
        "AWS_ACCESS_KEY_ID": "minioadmin",
        "AWS_SECRET_ACCESS_KEY": "minioadmin"
    }
}
```

## Key Dependencies

- controller-runtime: Kubernetes controller framework
- go-git: Git operations
- Helm SDK: Helm chart operations
- Cloud SDKs: AWS, Azure, GCP for bucket sources
- Cosign/Notation: Container signing verification

## Performance Considerations

- Helm cache TTL defaults to 15 minutes
- Artifact retention TTL defaults to 60 seconds
- Concurrent reconciliations default to 2 per controller
- Use `--requeue-dependency` to control reconciliation intervals