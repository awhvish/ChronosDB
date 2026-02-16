# Contributing to SisyphusDB

Thank you for your interest in contributing to SisyphusDB! This guide will help you get started with development, testing, and submitting your contributions.

## 📋 Table of Contents

- [Development Environment Setup](#development-environment-setup)
- [Project Structure](#project-structure)
- [Building and Running](#building-and-running)
- [Running Tests](#running-tests)
- [Code Style Guide](#code-style-guide)
- [Submitting a Pull Request](#submitting-a-pull-request)

---

## 🛠 Development Environment Setup

### Prerequisites

- **Go 1.22+** — [Download](https://go.dev/dl/)
- **Docker** & **Docker Compose** — [Install Docker](https://docs.docker.com/get-docker/)
- **kubectl** (optional, for Kubernetes deployment) — [Install kubectl](https://kubernetes.io/docs/tasks/tools/)
- **Protobuf Compiler** (`protoc`) — For gRPC code generation
  ```bash
  # macOS
  brew install protobuf

  # Ubuntu/Debian
  sudo apt install protobuf-compiler

  # Verify
  protoc --version
  ```

### Clone the Repository

```bash
git clone https://github.com/awhvish/SisyphusDB.git
cd SisyphusDB
```

### Install Go Dependencies

```bash
go mod download
go mod tidy
```

### Generate Protocol Buffers (if modifying `.proto` files)

```bash
protoc --go_out=. --go-grpc_out=. proto/*.proto
```

---

## 📁 Project Structure

SisyphusDB is organized into distinct modules for clarity and maintainability:

```
SisyphusDB/
├── api/                # HTTP server and REST endpoints
│   └── server.go       # Main HTTP server implementation
├── cmd/                # Entry points for binaries
│   ├── cli/            # CLI client (sicli)
│   └── server/         # Main SisyphusDB server
├── deploy/             # Deployment configurations
│   ├── docker/         # Docker Compose setup
│   └── k8s/            # Kubernetes manifests (StatefulSet, Services, ConfigMaps)
├── docs/               # Documentation and diagrams
│   ├── ARCHITECTURE.md # System design and data flow
│   ├── benchmarks/     # Performance test results
│   └── *.png           # Architecture diagrams
├── kv/                 # Core key-value storage engine (LSM Tree)
│   ├── db.go           # Main database interface
│   ├── memtable.go     # In-memory write buffer
│   ├── wal.go          # Write-Ahead Log
│   └── compaction.go   # SSTable compaction logic
├── pkg/                # Shared utilities and helpers
│   ├── logger/         # Logging configuration
│   └── metrics/        # Prometheus metrics
├── proto/              # Protocol Buffer definitions for gRPC
│   └── raft.proto      # Raft consensus messages
├── raft/               # Raft consensus implementation
│   ├── node.go         # Raft node logic
│   ├── log.go          # Raft log replication
│   └── election.go     # Leader election
├── sstable/            # SSTable (Sorted String Table) implementation
│   ├── reader.go       # SSTable reader
│   ├── writer.go       # SSTable writer
│   └── bloom.go        # Bloom filter for fast lookups
├── tests/              # Integration and chaos tests
│   ├── integration/    # Full cluster tests
│   └── chaos/          # Leader failover and partition tests
└── README.md           # Project overview
```

### Key Directories Explained

| Directory | Purpose |
|-----------|---------|
| **api/** | HTTP REST server that wraps the KV store |
| **cmd/** | Executable entry points (`main.go` files) |
| **kv/** | Core storage engine (MemTable, WAL, SSTables, compaction) |
| **raft/** | Distributed consensus (leader election, log replication) |
| **sstable/** | On-disk sorted tables with Bloom filters |
| **deploy/** | Docker and Kubernetes deployment scripts |
| **tests/** | Integration tests and chaos engineering scenarios |

---

## 🚀 Building and Running

### 1. Build the Server

```bash
go build -o sisyphusdb cmd/server/main.go
```

### 2. Run Locally (Single Node)

```bash
./sisyphusdb --node-id=node1 --port=8080
```

### 3. Run with Docker Compose (3-Node Cluster)

```bash
docker-compose up --build
```

Access the HTTP API at `http://localhost:8001`

### 4. Build the CLI Client

```bash
go build -o sicli cmd/cli/main.go
```

### 5. Use the CLI

```bash
# Configure server
sicli config set --server-url http://localhost:8080

# Basic operations
sicli put hello world
sicli get hello
sicli delete hello

# View metrics
sicli metrics
```

---

## 🧪 Running Tests

### Unit Tests

```bash
go test ./... -v
```

### Run Tests with Coverage

```bash
go test ./... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

### Integration Tests

```bash
cd tests/integration
go test -v
```

### Chaos Tests (Leader Failover)

```bash
cd tests/chaos
go test -v -run TestLeaderFailover
```

### Benchmarks

```bash
cd tests/benchmarks
go test -bench=. -benchmem
```

---

## 📝 Code Style Guide

### General Guidelines

- **Follow Go conventions**: Use `gofmt` and `golangci-lint`
- **Error handling**: Always check and handle errors; avoid silent failures
- **Comments**: Use clear, concise comments for public APIs and complex logic
- **Logging**: Use structured logging (`pkg/logger`)

### Formatting

Before committing, run:

```bash
gofmt -w .
go vet ./...
golangci-lint run
```

### Naming Conventions

| Type | Convention | Example |
|------|-----------|---------|
| **Packages** | Short, lowercase, no underscores | `raft`, `sstable` |
| **Files** | Lowercase with underscores | `mem_table.go` |
| **Interfaces** | Nouns ending in `-er` | `Compactor`, `LogReader` |
| **Functions** | Camel case, action verbs | `ApplyCommand()`, `ReplicateLog()` |
| **Constants** | Pascal case | `MaxRetries`, `DefaultTimeout` |

### Example: Adding a New Feature

1. **Create a feature branch**:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Write tests first** (TDD approach):
   ```go
   func TestNewFeature(t *testing.T) {
       // Arrange
       db := NewDB()
       
       // Act
       result := db.YourNewMethod()
       
       // Assert
       assert.Equal(t, expected, result)
   }
   ```

3. **Implement the feature**

4. **Ensure tests pass**:
   ```bash
   go test ./...
   ```

5. **Format and lint**:
   ```bash
   gofmt -w .
   golangci-lint run
   ```

---

## 🔄 Submitting a Pull Request

### 1. Fork the Repository

Click "Fork" on GitHub, then clone your fork:

```bash
git clone https://github.com/YOUR_USERNAME/SisyphusDB.git
cd SisyphusDB
git remote add upstream https://github.com/awhvish/SisyphusDB.git
```

### 2. Create a Feature Branch

```bash
git checkout -b feature/your-feature-name
```

### 3. Make Your Changes

- Write clean, well-tested code
- Add tests for new functionality
- Update documentation if needed

### 4. Commit Your Changes

```bash
git add .
git commit -m "feat: add support for X"
```

**Commit Message Format** (follow [Conventional Commits](https://www.conventionalcommits.org/)):

```
<type>: <description>

Examples:
feat: add bloom filter to SSTable reader
fix: resolve race condition in raft leader election
docs: update architecture diagram
test: add chaos test for network partition
refactor: simplify compaction logic
```

### 5. Push to Your Fork

```bash
git push origin feature/your-feature-name
```

### 6. Open a Pull Request

- Go to your fork on GitHub
- Click "Compare & pull request"
- Fill out the PR template with:
  - **Description** of changes
  - **Related issue** number (if applicable)
  - **Testing** performed
  - **Screenshots** (if UI changes)

### 7. Code Review

- Maintainers will review your PR
- Address feedback and push additional commits
- Once approved, your PR will be merged!

---

## 🤝 Community and Support

- **Issues**: [GitHub Issues](https://github.com/awhvish/SisyphusDB/issues)
- **Discussions**: [GitHub Discussions](https://github.com/awhvish/SisyphusDB/discussions)

Thank you for contributing to SisyphusDB! 🎉