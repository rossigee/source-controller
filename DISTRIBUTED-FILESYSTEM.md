# Distributed Filesystem Storage for Source Controller v2

## Overview

This document outlines the design for a distributed filesystem storage backend that enables horizontal scaling of the source-controller while using local filesystem storage. This approach uses peer-to-peer replication to ensure all pods have access to all artifacts.

## Problem Statement

Currently, when using filesystem storage with multiple replicas:
- Each pod stores artifacts locally in its own filesystem
- Artifacts stored by one pod are not accessible by others
- Requests load-balanced to different pods result in 404 errors
- This forces filesystem mode to run with a single replica

## Proposed Solution: Push-Based Peer Replication

### Architecture

1. **Peer Discovery**
   - Use Kubernetes Endpoints/EndpointSlice API to discover peer pods
   - Leverage existing leader election mechanism for peer awareness
   - Maintain a dynamic list of healthy peers

2. **Push Replication**
   - When a pod stores an artifact, it immediately pushes to all peers
   - Asynchronous replication for performance
   - Eventually consistent model

3. **Storage Backend**
   - New backend type: `distributed-filesystem`
   - Extends existing filesystem storage with replication
   - Maintains backward compatibility

### Implementation Design

#### New Backend Type

```go
const (
    BackendFilesystem            BackendType = "filesystem"
    BackendS3                    BackendType = "s3"
    BackendDistributedFilesystem BackendType = "distributed-filesystem"
)
```

#### Core Components

##### DistributedFilesystemStorage

```go
type DistributedFilesystemStorage struct {
    *FilesystemStorage
    peers        *PeerDiscovery
    replicator   *PeerReplicator
    selfEndpoint string
    logger       logr.Logger
}

func (d *DistributedFilesystemStorage) Store(ctx context.Context, artifact *v1.Artifact, reader io.Reader) error {
    // Buffer content for replication
    var buf bytes.Buffer
    tee := io.TeeReader(reader, &buf)
    
    // Store locally first
    if err := d.FilesystemStorage.Store(ctx, artifact, tee); err != nil {
        return err
    }
    
    // Replicate to peers asynchronously
    go d.replicateToPeers(ctx, artifact, buf.Bytes())
    
    return nil
}

func (d *DistributedFilesystemStorage) replicateToPeers(ctx context.Context, artifact *v1.Artifact, content []byte) {
    peers := d.peers.GetPeers()
    
    var wg sync.WaitGroup
    for _, peer := range peers {
        if peer.Address == d.selfEndpoint {
            continue
        }
        
        wg.Add(1)
        go func(p Peer) {
            defer wg.Done()
            
            ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
            defer cancel()
            
            if err := d.replicator.Push(ctx, p, artifact, content); err != nil {
                d.logger.Error(err, "failed to replicate to peer", 
                    "peer", p.Address, 
                    "artifact", artifact.Path)
            }
        }(peer)
    }
    
    // Wait with timeout
    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        d.logger.V(1).Info("replication completed", "artifact", artifact.Path)
    case <-time.After(60 * time.Second):
        d.logger.Error(nil, "replication timeout", "artifact", artifact.Path)
    }
}
```

##### PeerDiscovery

```go
type PeerDiscovery struct {
    client    kubernetes.Interface
    namespace string
    service   string
    selfPod   string
    logger    logr.Logger
    
    mu    sync.RWMutex
    peers []Peer
}

type Peer struct {
    Address  string
    PodName  string
    NodeName string
}

func (pd *PeerDiscovery) Start(ctx context.Context) error {
    // Initial discovery
    if err := pd.refresh(); err != nil {
        return err
    }
    
    // Watch for changes
    go pd.watch(ctx)
    
    return nil
}

func (pd *PeerDiscovery) GetPeers() []Peer {
    pd.mu.RLock()
    defer pd.mu.RUnlock()
    return append([]Peer{}, pd.peers...)
}

func (pd *PeerDiscovery) refresh() error {
    endpoints, err := pd.client.CoreV1().Endpoints(pd.namespace).Get(
        context.Background(), pd.service, metav1.GetOptions{})
    if err != nil {
        return err
    }
    
    var peers []Peer
    for _, subset := range endpoints.Subsets {
        for _, addr := range subset.Addresses {
            if addr.TargetRef != nil && addr.TargetRef.Name != pd.selfPod {
                peers = append(peers, Peer{
                    Address:  fmt.Sprintf("%s:9091", addr.IP), // Internal replication port
                    PodName:  addr.TargetRef.Name,
                    NodeName: addr.NodeName,
                })
            }
        }
    }
    
    pd.mu.Lock()
    pd.peers = peers
    pd.mu.Unlock()
    
    return nil
}
```

##### PeerReplicator

```go
type PeerReplicator struct {
    client     *http.Client
    authToken  string // Shared secret for peer authentication
    logger     logr.Logger
}

func (pr *PeerReplicator) Push(ctx context.Context, peer Peer, artifact *v1.Artifact, content []byte) error {
    url := fmt.Sprintf("http://%s/_internal/replicate", peer.Address)
    
    req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(content))
    if err != nil {
        return err
    }
    
    // Add artifact metadata as headers
    req.Header.Set("X-Artifact-Path", artifact.Path)
    req.Header.Set("X-Artifact-Revision", artifact.Revision)
    req.Header.Set("X-Artifact-Digest", artifact.Digest)
    req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", pr.authToken))
    req.Header.Set("Content-Type", "application/gzip")
    
    resp, err := pr.client.Do(req)
    if err != nil {
        return fmt.Errorf("replication request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("replication failed: %s - %s", resp.Status, string(body))
    }
    
    return nil
}
```

#### Server Extensions

```go
func (s *ArtifactServer) Handler() http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("/", s.serveArtifact)
    mux.HandleFunc("/health", s.healthCheck)
    mux.HandleFunc("/_internal/replicate", s.handleReplication)
    return mux
}

func (s *ArtifactServer) handleReplication(w http.ResponseWriter, r *http.Request) {
    // Only allow PUT/POST
    if r.Method != http.MethodPut && r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    
    // Verify peer authentication
    if !s.verifyPeerAuth(r) {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }
    
    // Extract artifact metadata
    artifact := &v1.Artifact{
        Path:     r.Header.Get("X-Artifact-Path"),
        Revision: r.Header.Get("X-Artifact-Revision"),
        Digest:   r.Header.Get("X-Artifact-Digest"),
    }
    
    // Store without triggering further replication
    if ds, ok := s.provider.(*DistributedFilesystemStorage); ok {
        // Direct store to avoid replication loop
        if err := ds.FilesystemStorage.Store(r.Context(), artifact, r.Body); err != nil {
            s.logger.Error(err, "replication store failed", "path", artifact.Path)
            http.Error(w, "Storage failed", http.StatusInternalServerError)
            return
        }
    } else {
        http.Error(w, "Not a distributed filesystem", http.StatusBadRequest)
        return
    }
    
    w.WriteHeader(http.StatusOK)
}

func (s *ArtifactServer) verifyPeerAuth(r *http.Request) bool {
    // Verify the request is from a known peer
    // Options:
    // 1. Shared secret in Authorization header
    // 2. mTLS with peer certificates
    // 3. Source IP validation against known endpoints
    
    token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
    return token == s.peerAuthToken
}
```

### Configuration

```yaml
# Deployment configuration for distributed filesystem mode
apiVersion: v1
kind: ConfigMap
metadata:
  name: source-controller-config
data:
  storage-backend: "distributed-filesystem"
  storage-path: "/data"
  replication-port: "9091"
  peer-auth-token: "${PEER_AUTH_TOKEN}" # From secret

---
apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 3  # Can now scale horizontally
  template:
    spec:
      containers:
      - name: source-controller
        args:
        - --storage-backend=distributed-filesystem
        - --storage-path=/data
        - --replication-port=9091
        ports:
        - containerPort: 9090  # Artifact server
          name: http
        - containerPort: 9091  # Replication endpoint
          name: replication
```

### Operational Considerations

#### Startup Synchronization
When a new pod joins the cluster, it needs to synchronize existing artifacts:
1. Query peers for artifact list
2. Pull missing artifacts from peers
3. Mark itself as ready only after initial sync

#### Storage Management
- Implement artifact garbage collection across all peers
- Consider storage quotas per pod
- Optional: Implement LRU eviction for old artifacts

#### Network Partitions
- Artifacts may be temporarily inconsistent during network splits
- Healing process when partition resolves
- Consider using a gossip protocol for robustness

#### Security
- Peer authentication using shared secrets or mTLS
- Verify source IPs match known pod endpoints
- Consider encryption for sensitive artifacts

### Benefits

1. **Horizontal Scaling**: Run multiple replicas with filesystem storage
2. **High Availability**: Any pod can serve any artifact
3. **Performance**: Local serving after replication
4. **Simplicity**: No external dependencies like S3
5. **Cost**: No cloud storage costs

### Limitations

1. **Storage Overhead**: Each artifact stored N times (N = replicas)
2. **Network Traffic**: Replication traffic between pods
3. **Eventual Consistency**: Brief period where artifact may not be on all pods
4. **Complexity**: More complex than single replica or S3

### Migration Path

1. Start with `storage-backend: filesystem` (single replica)
2. Implement distributed filesystem backend
3. Test thoroughly with small replica count
4. Gradually increase replicas monitoring replication lag
5. Consider S3 for very large deployments

### Alternative Approaches Considered

1. **Pull-based discovery**: Query peers on cache miss
   - Rejected due to latency and complexity
   
2. **Shared filesystem** (NFS, GlusterFS):
   - Rejected due to operational complexity and SPOF
   
3. **External cache** (Redis, Memcached):
   - Rejected as it recreates the S3 dependency problem

## Implementation Timeline

1. **Phase 1**: Peer discovery using Endpoints API
2. **Phase 2**: Basic push replication
3. **Phase 3**: Startup synchronization
4. **Phase 4**: Monitoring and metrics
5. **Phase 5**: Production hardening

## Conclusion

The distributed filesystem backend provides a middle ground between simple single-replica filesystem storage and full S3 integration. It enables horizontal scaling while keeping infrastructure dependencies minimal.