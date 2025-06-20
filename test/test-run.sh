#!/usr/bin/env bash
set -euo pipefail

INBOX_NS="cloud-operator-cr-queue"
echo "Seeding ExampleJobs into namespace '${INBOX_NS}'..."
for i in {1..20}; do
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