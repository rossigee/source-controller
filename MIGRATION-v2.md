# Migration Guide: Source Controller v2.0

## Overview

Source Controller v2.0 introduces distributed artifact serving to solve the scalability limitations of v1.x where only the leader pod could serve artifacts. This guide explains how to migrate from v1.x to v2.0.

## Key Changes

### 1. All Pods Now Serve Artifacts
- **v1.x**: Only the leader pod serves artifacts; non-leader pods are not ready
- **v2.0**: All pods can serve artifacts and are marked as ready
- **Impact**: Better load distribution, improved availability, true horizontal scaling

### 2. Storage Backend Options
- **v1.x**: Local filesystem only
- **v2.0**: Choice of backends:
  - `filesystem`: Local filesystem (default, backwards compatible)
  - `s3`: AWS S3 or compatible (MinIO, GCS, etc.)

### 3. New Configuration Flags
```bash
--storage-backend        # Storage backend type (filesystem, s3)
--s3-bucket             # S3 bucket name (required for s3 backend)
--s3-prefix             # S3 key prefix for artifacts
--s3-region             # S3 region (default: us-east-1)
--s3-endpoint           # Custom S3 endpoint (for MinIO, etc.)
--s3-force-path-style   # Force path-style URLs (for MinIO)
```

## Migration Scenarios

### Scenario 1: Keep Using Filesystem Storage (Default)

No changes required! The default configuration maintains full backwards compatibility:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: source-controller
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --storage-path=/data
        - --storage-backend=filesystem  # Optional, this is the default
```

### Scenario 2: Migrate to S3 Storage

#### Step 1: Prepare S3 Bucket

```bash
# Create S3 bucket
aws s3 mb s3://flux-artifacts

# Set bucket policy for source-controller access
aws s3api put-bucket-policy --bucket flux-artifacts --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"AWS": "arn:aws:iam::ACCOUNT:role/source-controller"},
    "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"],
    "Resource": ["arn:aws:s3:::flux-artifacts/*", "arn:aws:s3:::flux-artifacts"]
  }]
}'
```

#### Step 2: Update Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: source-controller
spec:
  replicas: 3  # Can now scale horizontally!
  template:
    spec:
      containers:
      - name: manager
        args:
        - --storage-backend=s3
        - --s3-bucket=flux-artifacts
        - --s3-region=us-east-1
        env:
        - name: AWS_REGION
          value: us-east-1
```

#### Step 3: Migrate Existing Artifacts (Optional)

```bash
# Export existing artifacts
kubectl exec -n flux-system deployment/source-controller -- tar -czf - /data | tar -xzf - -C ./backup

# Upload to S3
aws s3 sync ./backup/data/ s3://flux-artifacts/ --exclude "*.lock"
```

### Scenario 3: Using MinIO

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: source-controller
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --storage-backend=s3
        - --s3-bucket=flux-artifacts
        - --s3-endpoint=http://minio.minio-system.svc.cluster.local:9000
        - --s3-force-path-style=true
        env:
        - name: AWS_ACCESS_KEY_ID
          valueFrom:
            secretKeyRef:
              name: minio-credentials
              key: accesskey
        - name: AWS_SECRET_ACCESS_KEY
          valueFrom:
            secretKeyRef:
              name: minio-credentials
              key: secretkey
```

## Rollback Plan

If you need to rollback to v1.x:

1. **Scale down to 1 replica** (only leader serves in v1.x)
2. **Switch back to v1.x image**
3. **Remove new flags** (they'll be ignored but may cause warnings)

```yaml
spec:
  replicas: 1  # Important: only 1 replica for v1.x
  template:
    spec:
      containers:
      - name: manager
        image: ghcr.io/fluxcd/source-controller:v1.x.x
        args:
        - --storage-path=/data
        # Remove --storage-backend and --s3-* flags
```

## Performance Tuning

### Horizontal Scaling
With v2.0, you can now effectively scale horizontally:

```yaml
spec:
  replicas: 5  # All 5 pods will serve artifacts
  
  # HPA also works well now
  ---
  apiVersion: autoscaling/v2
  kind: HorizontalPodAutoscaler
  metadata:
    name: source-controller
  spec:
    scaleTargetRef:
      apiVersion: apps/v1
      kind: Deployment
      name: source-controller
    minReplicas: 3
    maxReplicas: 10
    metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 80
```

### S3 Performance
- Use S3 Transfer Acceleration for global deployments
- Consider S3 Intelligent-Tiering for cost optimization
- Set appropriate `--s3-prefix` to organize artifacts

## Monitoring

### New Metrics (v2.0)
- `source_controller_storage_backend` - Current storage backend in use
- `source_controller_artifact_serve_duration_seconds` - Artifact serving latency
- `source_controller_storage_operations_total` - Storage operations by type

### Health Checks
The readiness probe now checks `/health` which verifies:
- HTTP server is responding
- Storage backend is accessible
- All pods are ready to serve (not just leader)

## FAQ

**Q: Do I need to change my GitRepository/HelmRepository resources?**
A: No, the API remains unchanged. This is purely an infrastructure improvement.

**Q: What happens to my existing artifacts?**
A: With filesystem backend, they remain untouched. For S3 migration, you can optionally copy them.

**Q: Can I mix storage backends?**
A: No, all source-controller pods must use the same storage backend.

**Q: Is the S3 backend more expensive?**
A: Depends on usage. S3 is cost-effective for large deployments with many artifacts. Use lifecycle policies to control costs.

**Q: What about network policies?**
A: For S3 backend, ensure pods can reach S3 endpoints. For filesystem, no changes needed.

## Support

For issues or questions:
- GitHub Issues: https://github.com/fluxcd/source-controller/issues
- Slack: #flux on CNCF Slack

## Next Steps

1. Test in staging environment first
2. Plan maintenance window (though rolling update is supported)
3. Monitor metrics after migration
4. Scale replicas based on load