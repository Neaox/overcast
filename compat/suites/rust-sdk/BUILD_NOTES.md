# Rust SDK Compat Suite Build Optimization

## BuildKit Cache Mounts

This Dockerfile uses Docker BuildKit **cache mounts** to dramatically speed up builds across rebuilds.

### First Build

- Takes longer as dependencies are downloaded and compiled
- Cargo registry cache is saved: `~/.cargo/registry`
- Build artifacts are saved: `target/`

### Subsequent Builds

- ⚡ **Much faster** (~30-60s) because:
  - Cargo registry is reused (no re-downloads)
  - Unchanged dependency builds are cached
  - Only source changes are recompiled

### Enable BuildKit

**Docker CLI** (auto-enabled in recent versions):

```bash
docker build -f compat/suites/rust-sdk/Dockerfile -t oc-rust-sdk-compat:latest compat/suites
```

**Docker Compose** (add to docker-compose.dev.yml):

```yaml
version: "3.8"
services:
  rust-compat:
    build:
      context: .
      dockerfile: compat/suites/rust-sdk/Dockerfile
      # Explicit BuildKit settings (optional)
x-build-context:
  DOCKER_BUILDKIT: 1
```

### Run with BuildKit explicitly:

```bash
DOCKER_BUILDKIT=1 docker build -f compat/suites/rust-sdk/Dockerfile -t oc-rust-sdk-compat:latest compat/suites
```

## Performance Tips

1. **Reuse build cache**: Run builds without `--no-cache` for subsequent builds to benefit from caching
2. **Pin Cargo.lock**: Commit `Cargo.lock` to repository for reproducible, cached builds
3. **Avoid changing Cargo.toml**: Each change to `Cargo.toml` triggers fresh dependency resolution
4. **Use smaller base images**: `rust:1.91-alpine` is already minimal

## Cargo.lock

On first build, `Cargo.lock` is auto-generated. It should be committed to the repo:

```bash
git add compat/suites/rust-sdk/Cargo.lock
git commit -m "Add Cargo.lock for reproducible builds"
```

This ensures all developers and CI systems use identical dependency versions.
