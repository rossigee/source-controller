# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The source-controller is a Kubernetes operator in the Flux CD GitOps toolkit that specializes in artifact acquisition from external sources (Git repositories, OCI registries, Helm repositories, S3-compatible buckets). It reconciles source objects and makes their artifacts available via an HTTP file server.

## Critical Architecture Issue - Leader Election & Scalability

**Important**: The source-controller has a known scalability limitation where only the leader pod serves artifacts. Non-leader replicas are not ready and cannot serve HTTP requests. This creates a single point of failure/bottleneck. See commented code in main.go:304 and GitHub issue #837.

## Essential Commands

### Development
```bash
# Run controller locally for debugging
make install    # Install CRDs first
make run        # Run controller (add args: ARGS="--storage-adv-addr=:0 --storage-path=./bin/data")

# Run tests
make test                                                    # All tests
make test-ctrl                                              # Controller tests only
make test GO_TEST_ARGS="-v -run=TestSpecificTest"         # Specific test

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
- Artifacts stored at: `/data/<kind>/<namespace>/<name>/<filename>`
- HTTP server runs on port 9090 (configurable via `--storage-addr`)
- Implements garbage collection based on TTL and retention count
- Storage interface defined in `internal/controller/storage.go`

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

## Important Implementation Details

- The file server starts immediately in a goroutine (not gated by leader election)
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