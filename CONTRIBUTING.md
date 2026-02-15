# Contributing to KV-Store

Thank you for your interest in contributing to KV-Store!

We welcome contributions of all kinds—whether you're fixing bugs, improving performance, writing docs, or building new features. This guide will help you get started.

---

## How Can You Contribute?

---

## 📂 Project Structure

Navigating a new codebase can be daunting. Here is a quick map of where things live:

* **`cmd/`** – The "Front Door." Contains the main entry points for the server and CLI.
* **`internal/`** – The "Engine Room." Core logic that shouldn't be imported by other projects (Raft implementation, storage engine).
* **`pkg/`** – "Shared Tools." Helper libraries that are safe for external use.
* **`api/`** – The "Contract." Protobuf or interface definitions for node communication.
* **`tests/`** – The "Safety Net." Integration and chaos tests.

---

### Improve Write RPS

Help us push the limits of write throughput:

- **Optimize the write path** – Identify bottlenecks in the Raft log replication or WAL writes
- **Reduce contention** – Improve batching strategies and minimize lock contention
- **Profile and tune hot paths** – Use Go's `pprof` to find and optimize performance-critical code

If you have experience with high-performance Go systems, this is a great area to dive into.

---

### Chaos and Failure Testing

Build confidence in our system's resilience:

- **Write chaos test scripts** – Automate failure injection scenarios
- **Simulate failures** – Node crashes, network partitions, disk failures
- **Validate correctness** – Ensure data consistency under adverse conditions

Check out `tests/chaos/` for existing test patterns.

---

### Documentation

Make the project more accessible:

- **Improve existing docs** – Fix typos, clarify explanations, add examples
- **Document architecture** – Explain design decisions, trade-offs, and system internals
- **Onboarding guides** – Help new contributors get up to speed quickly

**Note:** Documentation-only changes can be submitted directly as PRs without opening an issue first.

---

### CLI Development

Build a command-line interface for the KV store:

- **Preferred library:** [Cobra](https://github.com/spf13/cobra)
- **Core commands:**
  - `get <key>` – Retrieve a value
  - `set <key> <value>` – Store a key-value pair
  - `delete <key>` – Remove a key
  - `cluster status` – View cluster health and node info

A well-designed CLI will make the project much more user-friendly.

---

## Getting Started

### 1. Fork and Clone

```bash
git clone https://github.com/<your-username>/KV-Store.git
cd KV-Store
```

### 2. Set Up Your Environment

```bash
go mod download
```

### 3. Run Tests

```bash
go test ./...
```

### 4. Make Your Changes

Create a new branch for your work:

```bash
git checkout -b feature/your-feature-name
```

---

## Submitting Your Contribution

### For Code Changes

1. **Open an issue first** – Describe what you're working on and discuss your approach
2. **Reference the issue** in your PR
3. **Ensure tests pass** – Add new tests if applicable
4. **Keep commits clean** – Use meaningful commit messages

### For Documentation Changes

- You can submit a PR directly without opening an issue
- Keep changes focused and well-formatted

---

## Code Style

- Follow standard Go conventions (`gofmt`, `golint`)
- Write clear, descriptive commit messages
- Add comments for non-obvious logic
- Include tests for new functionality

---

## Need Help?

- Open an issue with the `question` label
- Check existing issues and discussions

We look forward to your contributions.
