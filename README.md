# BalancerX

**BalancerX** is a Kubernetes controller that dispatches Custom Resources (CRs) across multiple worker namespaces based on pluggable load balancing strategies like Ring Hashing, Round Robin, or Least Connections. It is designed to evenly distribute CR workloads across namespaces in scalable Kubernetes environments.

---

## Features

- **Dispatch CRs** (e.g., `ExampleJob`) to labeled worker namespaces
- **Pluggable load balancing**:
    - `ringhash`: consistent hashing for stability
    - `roundrobin`: simple cycling
    - `leastconn`: assigns to the namespace with the fewest CRs
- **Dynamic namespace discovery** based on labels
- **Avoids duplicate dispatch** using annotations
- **Watches namespace changes** using informers

---

## Usage

### Prerequisites

- Kubernetes cluster (v1.29+)
- CRD installed for the target resource (e.g., `examplejobs.mygroup.io`)
- Go 1.23+ for development (if running from source)

### Run BalancerX

```sh
go run main.go \
  --group=mygroup.io \
  --version=v1 \
  --resource=examplejobs \
  --balancer=ringhash \
  --ns-label=balancer/worker=true
