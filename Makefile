SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

local-bulk-testing:
	# ---------- 0. CONFIG  ----------
    INBOX_NS="inbox"
    WORKER_NS=("worker-ns-1" "worker-ns-2" "worker-ns-3")
    CRD_NAME="examplejobs.mygroup.io"

    # ---------- 1. NAMESPACES  ----------
	@echo Creating namespaces...
	kubectl create namespace "${INBOX_NS}" --dry-run=client -o yaml | kubectl apply -f -
	for ns in "${WORKER_NS[@]}"; do
		kubectl create namespace "${ns}" --dry-run=client -o yaml | kubectl apply -f -
	done

	@echo Seeding ExampleJobs into namespace '${INBOX_NS}'...
	@for i in {1..40}; do
	@cat <<EOF | kubectl apply -f -
    apiVersion: mygroup.io/v1
    kind: ExampleJob
    metadata:
      name: job-$i
      namespace: ${INBOX_NS}
    spec:
      message: "Hello from job $i"
	@EOF
	@done

	@echo ExampleJobs seeded

