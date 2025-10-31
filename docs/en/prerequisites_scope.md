# Who this is for

DevOps/SRE/Platform engineers who operate Kubernetes and want CPU-based autoscaling for a single Deployment/StatefulSet using Golectra metrics.

## What you get

A Kubernetes controller with a namespaced CRD CPUBasedAutoscaler that:

* reads CPU% from Golectra `/api/v1/snapshot`,

* smooths it via SES (alpha, warmupPoints),

* computes replicas: `ceil(currentReplicas * S_t / targetCPUPercent)`,

* applies min/max limits, step limits, stabilization window, and a freeze mode,

* patches `spec.replicas` of the target workload,

* exposes api: `/healthz`, `/readyz`, `/metrics`, and `POST /api/v1/preview-plan`.

## Supported environment

* Kubernetes: 1.24–1.30+.

* Container runtime: any K8s-supported (containerd/CRI-O).

* Prometheus (optional) for scraping controller metrics.

* Golectra server reachable from controller Pod (HTTP).

## Access & RBAC

* The controller needs minimal permissions:

* Read/watch CPUBasedAutoscaler; update/status patch on it.

* Get/patch apps/{deployments,statefulsets}.

* Read HPAs/KEDA (to detect conflicts).

* Create/patch Events; use coordination.k8s.io/leases for leader election. 
Can run cluster-scoped (default) or namespace-scoped (set --namespace-scope).

## Resource budget

* Controller: CPU ≤ 100m, RAM ≤ 128Mi (configurable in Helm values).

* Cycle budget: each reconcile ≤ 0.5 × dtSeconds (emits an Event if exceeded).

## Non-functional constraints

* One scaler per workload: No HPA/KEDA must target the same Deployment/StatefulSet.

* Controller is deterministic and idempotent; it only patches spec.replicas.

* Assumes CPU% meaning in Golectra is per-pod utilization [0..100].