#!/bin/bash
set -e

GROUP="mycompany.io"
VERSION="v1alpha1"
SOURCE_NS="cloud-operator-cr-queue"

echo "🔧 Applying multiple CRDs in group: $GROUP..."

kubectl apply -f - <<EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: backups.${GROUP}
spec:
  group: ${GROUP}
  versions:
    - name: ${VERSION}
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                target:
                  type: string
                schedule:
                  type: string
  scope: Namespaced
  names:
    plural: backups
    singular: backup
    kind: Backup
    shortNames:
    - bkp
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: restores.${GROUP}
spec:
  group: ${GROUP}
  versions:
    - name: ${VERSION}
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                source:
                  type: string
                time:
                  type: string
  scope: Namespaced
  names:
    plural: restores
    singular: restore
    kind: Restore
    shortNames:
    - rst
EOF

echo "📦 Creating source namespace..."
kubectl create ns "${SOURCE_NS}" || true

echo "🚀 Applying test CRs (Backup and Restore)..."
for i in $(seq 1 10); do
  cat <<EOF | kubectl apply -f -
apiVersion: ${GROUP}/${VERSION}
kind: Backup
metadata:
  name: backup-${i}
  namespace: ${SOURCE_NS}
spec:
  target: "s3://bucket/backup-${i}"
  schedule: "0 ${i} * * *"
EOF

  cat <<EOF | kubectl apply -f -
apiVersion: ${GROUP}/${VERSION}
kind: Restore
metadata:
  name: restore-${i}
  namespace: ${SOURCE_NS}
spec:
  source: "s3://bucket/backup-${i}"
  time: "2024-01-01T00:00:00Z"
EOF
done

echo "⏳ Waiting for BalancerX to dispatch CRs..."
sleep 10

echo "📊 Results by Kind:"
kubectl get backups --all-namespaces -L balancer/target
kubectl get restores --all-namespaces -L balancer/target

echo "✅ Group-based multi-kind load balancing test complete."
