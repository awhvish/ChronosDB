# SisyphusDB

<p align="center">
  <b>A distributed key-value store built from scratch with Raft consensus</b>
</p>

<p align="center">
  <img src="docs/graphana_dashboard.png" width="80%" alt="Grafana Dashboard">
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="docs/ARCHITECTURE.md">Architecture</a> •
  <a href="#benchmarks">Benchmarks</a> •
  <a href="#chaos-testing">Chaos Testing</a> •
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

---

## Features

- **Raft Consensus** — Leader election, log replication, and automatic failover with <550ms recovery
- **LSM-Tree Storage** — LevelDB-style tiered compaction with Bloom filters for 95% fewer disk lookups
- **3,000+ Write RPS** — Achieved through batched RPCs, arena-based memory pooling, and async persistence
- **Kubernetes Native** — StatefulSet deployment with persistent volumes and Prometheus/Grafana monitoring
- **CLI Client** — Full-featured command-line interface with configuration management, metrics, and testing tools

---

## Quick Start

### CLI Client
```bash
# Build the CLI
go build -o sicli ./cmd/cli

# Basic operations
sicli put hello world
sicli get hello
sicli delete hello

# Configure server
sicli config set --server-url http://localhost:8081
sicli metrics
```

### Local (Docker Compose)
```bash
docker-compose up
# Access: http://localhost:8001/put?key=hello&val=world
# Grafana: http://localhost:3000 (admin/admin)
```

### Kubernetes
```bash
kubectl apply -f deploy/k8s/
```

See **[INSTALL.md](INSTALL.md)** for detailed setup and **[EKS-INSTALL.md](EKS-INSTALL.md)** for AWS deployment.

---

## Architecture

<p align="center">
  <img src="docs/high_level_architecture.png" width="70%" alt="Architecture">
</p>

SisyphusDB is a CP-compliant distributed system consisting of three layers:

| Layer | Components |
|-------|------------|
| **Consensus** | Raft leader election, log replication via gRPC, term-based conflict resolution |
| **Storage** | Write-ahead log, MemTable with arena allocator, SSTable compaction |
| **API** | HTTP interface with automatic leader forwarding |

See **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** for detailed documentation on system design, data flow, and consistency model.

---

## Benchmarks

### Write Performance

| Metric | Value |
|--------|-------|
| Peak Throughput | **2,960 RPS** |
| Mean Latency | 53.64ms |
| P99 Latency | 90.32ms |
| Success Rate | 100% |

### Memory Optimization

Custom arena allocator reduced write latency by **71%** (82ns → 23ns) by eliminating GC pressure.

| Implementation | Latency | Allocations/Op |
|----------------|---------|----------------|
| Standard Map | 82.21 ns | 0 B |
| Arena Allocator | 23.17 ns | 64 B |

Benchmarks: [docs/benchmarks/](docs/benchmarks/) | Arena profiling: [docs/benchmarks/arena/](docs/benchmarks/arena/)

---

## Chaos Testing

Chaos tests validate Raft's correctness guarantees under failure conditions.

### Test 1: Leader Failover

Kills the leader mid-write and verifies no acknowledged data is lost.

```
[Step 1] Start 3-node cluster
[Step 2] Write 50 keys
[Step 3] SIGKILL the leader
[Step 4] Wait for new leader election (<550ms)
[Step 5] Write 50 more keys
[Step 6] Verify all 100 keys exist
```

**Result:** ✅ Zero data loss | ✅ 472ms failover time

### Test 2: Network Partition

Simulates a split-brain scenario where nodes cannot communicate.

```
[Step 1] Start 5-node cluster
[Step 2] Partition nodes into {1,2} and {3,4,5}
[Step 3] Write to minority partition (nodes 1,2)
[Step 4] Verify writes fail (no quorum)
[Step 5] Write to majority partition (nodes 3,4,5)
[Step 6] Verify writes succeed
```

**Result:** ✅ Minority partition rejects writes | ✅ Majority partition accepts writes

See [tests/chaos/](tests/chaos/) for full test suite.

---

## Tech Stack

| Component | Technology |
|-----------|-----------|
| **Language** | Go 1.22+ |
| **Consensus** | Raft (custom implementation) |
| **Storage** | LSM Tree with Bloom Filters |
| **Networking** | gRPC (Protocol Buffers) |
| **Deployment** | Docker, Kubernetes (StatefulSets) |
| **Monitoring** | Prometheus + Grafana |

---

## Contributing

We welcome contributions! Please see **[CONTRIBUTING.md](CONTRIBUTING.md)** for:

- Development environment setup
- Project structure and folder explanations
- Building and running locally
- Testing guidelines
- Code style guide
- PR submission process

---

## Documentation

| Document | Description |
|----------|-------------|
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design, data flow, consistency model |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development guide and coding standards |
| [INSTALL.md](INSTALL.md) | Local and Docker setup instructions |
| [EKS-INSTALL.md](EKS-INSTALL.md) | AWS EKS deployment guide |

---

## License

MIT License — See [LICENSE](LICENSE) for details.

---

<p align="center">
  Built with ❤️ by <a href="https://github.com/awhvish">awhvish</a>
</p>