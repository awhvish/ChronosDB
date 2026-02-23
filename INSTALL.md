# SisyphusDB Installation & Setup Guide

This guide details how to build, deploy, and test SisyphusDB in local, Docker, and Kubernetes environments.

## 🛠️ Prerequisites

- **Go:** 1.25+

- **Docker:** 24.0+

- **Kubernetes:** Minikube, Kind, or a Cloud Provider (EKS/GKE)

- **Vegeta:** (Optional, for load testing)


---

##  Option 1: Local Development (Manual Cluster)

The most reliable way to debug locally is running 3 separate processes. **Crucial:** Node IDs must match the port template pattern to prevent routing loops.

### 1. Build the Binary (Optional)

Bash

```
go mod download
go build -o kv-server ./cmd/server/
```

### 2. Run a 3-Node Cluster

Open 3 separate terminal tabs and run the following commands. Note the HTTP port alignment (ID 0 -> 8000, ID 1 -> 8001, etc.).

**Terminal 1 (Node 0 - Leader Candidate)**

Bash

```
go run ./cmd/server/ \
  -id 0 \
  -port 5001 \
  -http 8000 \
  -peers :5001,:5002,:5003 \
  -peer-template "http://localhost:800%d"
```

**Terminal 2 (Node 1)**

Bash

```
go run ./cmd/server/ \
  -id 1 \
  -port 5002 \
  -http 8001 \
  -peers :5001,:5002,:5003 \
  -peer-template "http://localhost:800%d"
```

**Terminal 3 (Node 2)**

Bash

```
go run ./cmd/server/ \
  -id 2 \
  -port 5003 \
  -http 8002 \
  -peers :5001,:5002,:5003 \
  -peer-template "http://localhost:800%d"
```

> **Note:** By default, HTTP request logging is disabled for better performance. To enable request logging (useful for debugging), add the `-log-requests` flag:
> ```
> go run ./cmd/server/ -id 0 -port 5001 -http 8000 -peers :5001,:5002,:5003 -peer-template "http://localhost:800%d" -log-requests
> ```

### 3. Verify Connectivity

Test a write to the Leader (usually Node 0 or 1):

Bash

```
curl "http://localhost:8000/put?key=test&val=success"
```

---

## 🐳 Option 2: Docker Compose

Spin up a containerized 3-node cluster for integration testing.

Bash

```
# Start the cluster
docker-compose up --build -d

# View logs
docker-compose logs -f
```

---

## ☸️ Option 3: Kubernetes Deployment

Deploy SisyphusDB as a StatefulSet with stable network identities.

### 1. Build & Load Image

If using Minikube or Kind, load the image directly to the node cache so K8s can find it:

Bash

```
docker build -t sisyphusdb:v1 .
minikube image load sisyphusdb:v1
```

### 2. Apply Manifests

Deploy the Headless Service, StatefulSet, and ConfigMaps:

Bash

```
kubectl apply -f deploy/k8s
```

### 3. Verify Deployment

Wait for all 3 pods to become Ready (1/1):

Bash

```
kubectl get pods -l app=sisyphusdb -w
```

_Expected Output:_

Plaintext

```
NAME   READY   STATUS    RESTARTS   AGE
kv-0   1/1     Running   0          45s
kv-1   1/1     Running   0          30s
kv-2   1/1     Running   0          15s
```

---

## 🧪 Benchmarking & Performance

**Warning:** Do not use `kubectl port-forward` for load testing. It acts as a single-threaded bottleneck and will timeout at >150 RPS.

To reproduce the **2,000+ RPS** benchmark, run the load generator **inside** the cluster:

### 1. Launch a Shell inside the Cluster

Bash

```
kubectl run vegeta-shell --rm -i --tty --image=peterevans/vegeta -- sh
```

### 2. Run the Stress Test

Once inside the pod prompt (`/ #`), run the attack against the Service DNS:

Bash

```
# Attack the Leader (kv-0) at 3,000 RPS
echo "GET http://kv-0.kv-raft:8001/put?key=load&val=test" | vegeta attack -duration=5s -rate=3000 | vegeta report
```

### 3. (Optional) Local Benchmarking

If running **Option 1 (Manual Cluster)**, you can test directly from your host machine:

Bash

```
echo "GET http://localhost:8000/put?key=load&val=test" | vegeta attack -duration=5s -rate=2000 | vegeta report
```