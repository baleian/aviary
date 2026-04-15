# Agent Runtime Pool Catalog

This directory is the **single source of truth** for agent runtime pools.
Service-team code never creates per-agent Deployments, PVCs, NetworkPolicies,
or security groups — every agent runs inside one of the pools declared here.

A "pool" is the pair **(egress environment, runtime image)**. Two pools with
the same image but different egress are two separate deployments here.
Service-team's `agents` table carries a `pool_name` column; the API server
routes each session to `http://env-{pool_name}.agents.svc:3000` via the
agent-supervisor streaming proxy.

## File layout

| File | Purpose |
|---|---|
| `_shared-workspace.yaml` | Namespace-wide PV + PVC mounted at `/agent-platform` in every pool pod. Not a pool — prefix `_` marks shared infra. |
| `default.yaml` | Baseline pool. Platform-internal egress only (LLM, MCP, API, DNS). |
| `public-web.yaml` | Open-internet pool for agents that browse the web or install from public registries. |
| `{new-pool}.yaml` | Add one file per new pool. |

## Adding a new pool

1. Copy `default.yaml` to `{pool-name}.yaml`.
2. In the new file, replace every `env-default` → `env-{pool-name}` and the
   `aviary/pool: default` label → `aviary/pool: {pool-name}`.
3. Adjust **egress rules** in the NetworkPolicy (this is usually the only
   interesting delta). Add `to:` blocks for the CIDRs / namespaces / ports
   the pool should be allowed to reach.
4. Optionally swap the container `image:` if this pool needs a custom
   runtime build (e.g. with extra native deps). Image tags must be
   pre-approved and available in the registry.
5. Tune `replicas:`, `resources:`, and `terminationGracePeriodSeconds:` as
   needed.
6. `kubectl apply -f k8s/platform/pools/` (or let ArgoCD sync).

The service team then exposes the new pool in the admin UI's dropdown by
adding `"{pool-name}"` to their pool catalog list (DB / config).

## What NOT to do

- **Don't create per-agent resources here.** Agents are rows in the service
  team's DB; they never get their own Deployment.
- **Don't reference ServiceAccount secrets across pools.** Each pool carries
  its own SA so AWS IRSA / secret mounting can be scoped.
- **Don't open ingress from anywhere but the `agent-supervisor` Pod** — all
  callers (chat API, Temporal workers) go through the supervisor.

## Local dev vs production

- Local (K3s): `_shared-workspace.yaml` uses a hostPath PV backed by the
  `agent_workspace_pv` docker volume. `imagePullPolicy: Never` because
  `setup-dev.sh` side-loads images into containerd.
- Production (EKS, whether node-group or Fargate): replace
  `_shared-workspace.yaml` with an EFS CSI dynamic PVC (ReadWriteMany).
  Flip `imagePullPolicy` to `IfNotPresent` and change image refs to your
  ECR registry. Runtime code and every other pool file stay identical.
