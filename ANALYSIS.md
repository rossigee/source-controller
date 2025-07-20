# Source Controller Architecture Analysis

## Executive Summary

The Flux source-controller currently faces a critical scalability limitation: only the elected leader pod can serve artifacts via HTTP, causing all non-leader replicas to be marked as "not ready". This creates a single point of failure and prevents horizontal scaling. This document analyzes the current architecture and recommends an approach for v2.0 that addresses these limitations.

## Current Architecture

### Overview

The source-controller is a Kubernetes operator that:
1. Watches source objects (GitRepository, HelmRepository, OCIRepository, Bucket, HelmChart)
2. Fetches content from external sources
3. Creates tar.gz artifacts with consistent checksums
4. Stores artifacts on local filesystem
5. Serves artifacts via HTTP for consumption by other Flux components

### Storage System

The storage implementation (`internal/controller/storage.go`) is a local filesystem-based system with these characteristics:

**Core Components:**
- **Storage struct**: Manages artifact lifecycle on local filesystem
- **BasePath**: Root directory for artifact storage (`/data` in containers)
- **Hostname**: Used for generating artifact URLs
- **HTTP Server**: Simple Go `http.FileServer` serving the BasePath

**Storage Operations:**
- `Archive()`: Creates tar.gz from directories with deterministic checksums
- `Copy()`: Atomic file operations using temp files + rename
- `Lock()`: File-based locking for concurrent access
- `GarbageCollect()`: TTL and count-based cleanup

**Artifact Structure:**
```
/data/
├── <kind>/
│   └── <namespace>/
│       └── <name>/
│           ├── <revision>.tar.gz
│           └── latest.tar.gz (symlink)
```

### The Leader Election Problem

Located in `main.go:298-306`:

```go
go func() {
    // Block until our controller manager is elected leader. We presume our
    // entire process will terminate if we lose leadership, so we don't need
    // to handle that.
    // (bad assumption?) pods can come in and out of service, and replicas should
    // be ready to serve at all times! (https://github.com/fluxcd/source-controller/issues/837)
    // <-mgr.Elected()
    
    startFileServer(storage.BasePath, storageAddr)
}()
```

**Current Behavior:**
- HTTP server starts immediately (leader election check is commented out)
- But the readiness probe only succeeds for the leader
- Non-leader pods can't serve artifacts, marked as not ready
- All traffic routes to single leader pod

**Issues:**
1. **Single Point of Failure**: If leader pod dies, no artifacts can be served
2. **Network Locality**: Leader might be on a node with poor connectivity
3. **No Load Distribution**: All requests hit one pod regardless of replica count
4. **Scaling Inefficiency**: Adding replicas doesn't improve serving capacity

### Reconciler Patterns

All reconcilers follow similar patterns:

1. **Storage Initialization**: Each reconciler gets a Storage instance
2. **Reconciliation Flow**:
   ```
   reconcile() → reconcileStorage() → reconcileSource() → reconcileArtifact()
   ```
3. **Artifact Management**:
   - Check if artifact exists and verify digest
   - Create new artifacts when source changes
   - Update status with artifact URLs
   - Run garbage collection

**Key Coupling Points:**
- Direct use of Storage struct (not interface)
- Filesystem paths embedded in reconciliation logic  
- URL generation tied to Storage implementation
- Status conditions directly reference storage operations

## Analysis: Refactor vs Reimplement

### Option 1: Full Reimplement

**Pros:**
- Clean slate to design distributed-first architecture
- No legacy code constraints
- Can optimize for cloud-native storage from start

**Cons:**
- High risk of breaking changes
- Longer development timeline
- Need to maintain v1.x during transition
- May lose battle-tested edge cases

### Option 2: Pure Refactor

**Pros:**
- Preserves existing logic and edge cases
- Incremental changes possible
- Lower risk of regressions

**Cons:**
- Constrained by current architecture
- Harder to achieve optimal design
- Technical debt may persist

### Recommended Approach: Strategic Hybrid

**Refactor these components:**
- Extract Storage interface from concrete implementation
- Decouple URL generation from storage backend
- Standardize error handling across reconcilers
- Make reconcilers storage-agnostic

**Reimplement these components:**
- Artifact serving layer (distributed HTTP gateway)
- Storage backend (support S3/GCS/Azure/distributed cache)
- Garbage collection (distributed coordination)
- Health checks (storage-aware readiness)

**Preserve these components:**
- Reconciler business logic
- Source fetching logic
- Artifact packaging (tar.gz format)
- API contracts and status conditions

## Proposed Architecture for v2.0

### Storage Interface

```go
type Storage interface {
    // Artifact operations
    Store(ctx context.Context, artifact *v1.Artifact, content io.Reader) error
    Retrieve(ctx context.Context, artifact *v1.Artifact) (io.ReadCloser, error)
    Exists(ctx context.Context, artifact *v1.Artifact) (bool, error)
    Delete(ctx context.Context, artifact *v1.Artifact) error
    
    // Listing and GC
    List(ctx context.Context, filter ArtifactFilter) ([]*v1.Artifact, error)
    GarbageCollect(ctx context.Context, policy GCPolicy) error
}
```

### Backend Implementations

1. **S3/S3-Compatible** (recommended default):
   - Proven distributed storage
   - Works with MinIO for on-premise
   - Native versioning and lifecycle policies
   - Cost-effective for large deployments

2. **Shared Filesystem** (migration path):
   - NFS/ReadWriteMany PVC
   - Easier migration from v1.x
   - Limitations on some cloud providers

3. **Distributed Cache** (advanced):
   - Hazelcast/Redis cluster
   - Built-in replication
   - Memory-based for performance
   - Requires persistent backup

### Artifact Serving Options

1. **Direct Backend Access**:
   - Pods generate pre-signed URLs
   - Clients fetch directly from storage
   - Best scalability

2. **Distributed Gateway**:
   - All pods can serve any artifact
   - Caching layer for performance
   - Backwards compatibility with v1.x URLs

### Migration Strategy

1. **Phase 1**: Interface extraction (v1.x)
   - Extract Storage interface
   - Keep filesystem implementation
   - No breaking changes

2. **Phase 2**: Multi-backend support (v1.x)
   - Add S3 backend option
   - Feature flag for backend selection
   - Gradual rollout

3. **Phase 3**: Distributed serving (v2.0)
   - Remove leader-only serving
   - All pods ready to serve
   - Breaking change with migration guide

## Performance Considerations

**Current Limitations:**
- File I/O bottleneck on single node
- No caching between requests
- GC blocks on large directories

**v2.0 Improvements:**
- Distributed I/O across storage backend
- CDN/cache compatibility
- Parallel GC operations
- Horizontal scaling for serving

## Security Implications

**New Considerations:**
- Storage backend authentication
- Pre-signed URL expiration
- Network policies for backend access
- Encryption at rest configuration

## Conclusion

The recommended hybrid approach balances innovation with stability. By refactoring the storage abstraction while reimplementing the serving layer, we can:

1. Solve the leader election scalability issue
2. Enable true horizontal scaling
3. Maintain API compatibility where possible
4. Provide a clear migration path

This positions source-controller for cloud-native scalability while respecting its critical role in the Flux ecosystem.