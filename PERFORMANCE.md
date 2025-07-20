# Performance Analysis: Source Controller v2.0

## Executive Summary

The v2.0 architecture addresses the core scalability bottleneck by enabling horizontal scaling of artifact serving. This document evaluates the performance implications and provides benchmarking guidance.

## Performance Improvements

### 1. Load Distribution
- **v1.x**: All requests hit single leader pod
- **v2.0**: Requests distributed across all pods
- **Impact**: Linear scaling with replica count

### 2. Network Locality
- **v1.x**: Leader pod may be on distant node
- **v2.0**: Kubernetes load balancing chooses nearest pod
- **Impact**: Reduced latency, especially in multi-zone clusters

### 3. Storage Backend Performance

#### Filesystem (Default)
- **Pros**: 
  - Zero latency for local reads
  - No network overhead
  - Simple caching by OS
- **Cons**:
  - Limited to single node storage
  - No true distribution
- **Best for**: Small deployments, backwards compatibility

#### S3/MinIO
- **Pros**:
  - True distributed access
  - Pre-signed URLs offload serving
  - CDN compatibility
  - Infinite horizontal scaling
- **Cons**:
  - Network latency on first access
  - Storage costs
- **Best for**: Large deployments, multi-region

### 4. Resource Utilization

**CPU Usage**:
- v1.x: Leader pod CPU spikes during high load
- v2.0: CPU distributed evenly across pods

**Memory Usage**:
- v1.x: Leader pod holds all file handles
- v2.0: Memory usage distributed
- S3 backend: Minimal memory per pod (no file caching)

**Network I/O**:
- v1.x: Leader pod network bottleneck
- v2.0: Network load distributed
- S3 backend: Clients fetch directly from S3

## Benchmarking Recommendations

### Test Scenarios

1. **Artifact Serving Throughput**
```bash
# Test concurrent artifact downloads
for i in {1..100}; do
  curl -s http://source-controller:9090/gitrepository/default/app/latest.tar.gz > /dev/null &
done
wait
```

2. **Latency Under Load**
```bash
# Measure p50, p95, p99 latencies
vegeta attack -duration=30s -rate=100 \
  -targets=<(echo "GET http://source-controller:9090/gitrepository/default/app/latest.tar.gz") | \
  vegeta report -type=text
```

3. **Horizontal Scaling Test**
```bash
# Scale replicas and measure throughput
for replicas in 1 3 5 10; do
  kubectl scale -n flux-system deployment/source-controller --replicas=$replicas
  kubectl wait -n flux-system deployment/source-controller --for=condition=Available
  # Run throughput test
done
```

### Key Metrics to Monitor

1. **HTTP Metrics**
   - `source_controller_artifact_serve_duration_seconds` - Serving latency
   - `source_controller_http_requests_total` - Request rate
   - `source_controller_http_request_errors_total` - Error rate

2. **Storage Metrics**
   - `source_controller_storage_operations_total` - Storage ops by type
   - `source_controller_storage_operation_duration_seconds` - Storage latency
   - `source_controller_storage_backend` - Active backend

3. **Resource Metrics**
   - CPU usage per pod
   - Memory usage per pod
   - Network I/O per pod

## Expected Performance Gains

### Small Deployments (< 100 repos)
- **Filesystem backend**: 10-20% improvement from better load distribution
- **S3 backend**: Similar performance, better availability

### Medium Deployments (100-1000 repos)
- **Filesystem backend**: 2-3x throughput with 3 replicas
- **S3 backend**: 3-5x throughput with pre-signed URLs

### Large Deployments (> 1000 repos)
- **Filesystem backend**: Limited by storage I/O
- **S3 backend**: 10x+ throughput possible with proper S3 configuration

## Optimization Tips

### For Filesystem Backend
1. Use SSD storage for better I/O
2. Increase replica count to distribute load
3. Use pod anti-affinity for node distribution

### For S3 Backend
1. Enable S3 Transfer Acceleration for global deployments
2. Use S3 Intelligent-Tiering for cost optimization
3. Configure CloudFront CDN for edge caching
4. Set appropriate pre-signed URL expiration (default: 15m)

### General Optimizations
1. Tune garbage collection parameters
2. Adjust reconciliation intervals based on change frequency
3. Use horizontal pod autoscaling

## Cost Considerations

### Filesystem Backend
- **Storage**: Local disk cost only
- **Network**: Intra-cluster traffic only
- **Compute**: More replicas = more CPU/memory

### S3 Backend
- **Storage**: $0.023/GB/month (S3 Standard)
- **Requests**: $0.0004 per 1000 GET requests
- **Transfer**: $0.09/GB egress (can be optimized with CDN)
- **Compute**: Fewer replicas needed for same throughput

## Conclusion

The v2.0 architecture enables true horizontal scaling, with performance gains directly proportional to replica count. S3 backend provides the best scalability for large deployments, while filesystem backend maintains simplicity for smaller installations.