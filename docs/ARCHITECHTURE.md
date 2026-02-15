# SisyphusDB: System Architecture & Design

This document details the architectural decisions, internal data flows, consistency models, and the engineering roadmap of SisyphusDB. The system is designed as a coordinated fleet, transforming a simple KV store into a distributed system compliant with the CAP theorem (CP system).

## 1. High-Level System Architecture

The final system consists of a cluster of nodes (typically 3 or 5) functioning as a coordinated fleet. The architecture is composed of three distinct layers:

### Layer 1: The API & Network Layer (Entry Point)
- **gRPC Interface:** Uses Protocol Buffers for strict schemas and high-performance serialization.

### Layer 2: The Distribution Layer (The "Brain")
- **Raft Consensus Module:** Manages replication and leader election.
    - **Leader Election:** Automatically detects failures and elects a new leader.
    - **Log Replication:** Ensures data is confirmed by a majority before success.

### Layer 3: The Storage Layer (The "Muscle")
- **LSM Tree Engine:** The pipeline of `MemTable` → `WAL` → `SSTable`.
- **Compactor:** Merges old SSTables to reclaim space and solve amplification.

---

## 2. The Data Flow ("Life of a Request")

### The Write Path (PUT)
1. **Consensus (Raft):** Leader appends to Raft Log and broadcasts to Followers.
2. **Commit:** Once a Quorum of ACKs is received, the entry is marked Committed.
3. **Execution:** Leader applies entry to local LSM Tree (MemTable/WAL).
4. **Response:** Leader replies "success" to the client.

### The Read Path (GET)
1. **Routing:** Request hits the Leader (Strong) or Follower (Eventual).
2. **MemTable Check:** Checks active/immutable MemTables in RAM.
3. **Bloom Filter:** Queries SSTable filters to avoid unnecessary disk seeks.
4. **SSTable Search:** Performs binary search on disk-resident tables.

---

## 3. The "Parallel Worlds" Model (Consistency Internals)

To achieve **Linearizability** without blocking the internal Raft loop, we use Go Channels to bridge two decoupled processes:

- **`applyChan` (Raft → Store):** Delivers committed data.
- **`notifyChan` (Store → Client):** Wakes up the blocked client once the write is finalized.

---

## 4. High Availability: Smart Routing

SisyphusDB employs a production-grade routing strategy:
- **Primary Path:** Kubernetes Service routes directly to the Leader.
- **Fallback:** If a Follower receives a write, it **proxies** the request to the new Leader, ensuring zero-downtime during failover windows (<600ms).