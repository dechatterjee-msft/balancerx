#!/usr/bin/env bash
set -euo pipefail


INBOX_NS="inbox"
echo "Applying ExampleJob CRD..."
cat <<'EOF' | kubectl apply -f -
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: examplejobs.mygroup.io
spec:
  group: mygroup.io
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                message:
                  type: string
      subresources:
        status: {}
  scope: Namespaced
  names:
    plural: examplejobs
    singular: examplejob
    kind: ExampleJob
    shortNames:
      - ej
EOF


echo "Seeding ExampleJobs into namespace '${INBOX_NS}'..."
for i in {1..40}; do
cat <<EOF | kubectl apply -f -
apiVersion: mygroup.io/v1
kind: ExampleJob
metadata:
  name: job-$i
  namespace: ${INBOX_NS}
spec:
  message: "Hello from job $i"
EOF
done


kubectl get examplejobs -A -o json \
     	| jq -r '.items[].metadata.labels."balancer/target"' \
     	| sort | uniq -c

