# Testing notes

Everything in this repository was run end to end on **2026-10-04** before publishing. This page records what was used, what was checked, and what we changed in the lessons because of it.

## Environment

| Piece | Version |
| --- | --- |
| Cluster | k3d, single node, k3s `v1.37.1+k3s1` (arm64) |
| Client machine | Oracle Linux 9 (x86_64), kubectl and Kustomize v5.8.1 |
| Go | 1.26.0 |
| Kubebuilder | v4.16.0 |
| controller-runtime | v0.25.0 |
| envtest assets | Kubernetes 1.37.0 |
| Images | `nginx:stable`, `nginx:alpine`, `php:8.3-fpm`, `valkey/valkey:8.0` |
| Default StorageClass | `local-path` |

The lessons are written for kind; on k3d the only differences are the ones listed in the [README](README.md#your-playground-kind-or-k3d).

## What was run

| Day | Checked |
| --- | --- |
| 1 | All three YAML files, self-healing, owner references, ConfigMap refresh timing, scale drift, rolling update, bad image + `rollout undo`, selector typo |
| 2 | Hand-written CRD, defaults, all five validation errors, pruning with `--validate=false`, status subresource, CRD delete wiping CRs, scaffold, `make run`, all six experiments |
| 3 | Orphan and adopt, status/conditions/`observedGeneration`, events, `kubectl wait`, content-hash rollout, quiet `unchanged` passes |
| 4 | Missing Secret → `error`, Secret watch, gates (nginx only after php is ready), the page via port-forward, Secret rotation rolling only php, 502 with php removed, bad image, Secret deleted |
| 5 | Finalizer and directory cleanup, stuck deletion with the operator stopped, removing a finalizer by hand (leak confirmed), all three CEL rules, envtest suite, in-cluster deploy, RBAC `forbidden` with the Secrets marker removed, leader election failover, `v1beta1` storage version and `storedVersions`, clean-up order |
| 6 | Valkey StatefulSet by hand (ordered start, PVCs, DNS, replication, restart), KVCluster operator, write/read through the Services, backup Job, shrink refused / grow failing, missing StorageClass, split brain, PVC reuse after delete |

`reference/site-operator/setup.sh` was run from an empty directory: it scaffolds, copies the code, wires the recorders and finishes with `make test` passing (`internal/controller` ok, 57.6% coverage).

## Changes made to the lessons after testing

- **Day 1:** the page update after a ConfigMap edit took 40–70 seconds. `port-forward` stops when its Pod is deleted. With a selector typo the EndpointSlice shows `<unset>` and a new `port-forward` hangs rather than erroring.
- **Day 2:** Experiment 6 now lists the children by name (they carry no `app` label). Mentioned that the scaffold includes sample `Foo` and `Conditions` fields, and that the Service also reports `updated` every pass.
- **Day 3 and 4:** switched to the newer events API (`k8s.io/client-go/tools/events`, `mgr.GetEventRecorder`, RBAC on `events.k8s.io`). The new `Eventf` takes a *related* object and an *action*; passing the child as related keeps "Created ConfigMap" and "Created Deployment" from merging into one series.
- **Day 5:** custom resources don't show `Terminating` in `kubectl get`; check `deletionTimestamp`. All scaffolded test files need updating, not only PhpApp's. Added `k3d image import`. Clean-up now deletes `kv` and `kvbackup` too.
- **Day 6:** StorageClass names for kind and k3d. DNS check uses `getent` inside a member (the Valkey image has no `nslookup`). Experiment 5 now shrinks first, then grows, and explains how to recover since the size can't be patched back down. Replicas reconnect, and split brain heals, after a few seconds.

## Workarounds used only by the test environment

These were needed because the test cluster ran inside a nested container. You won't need them on a normal laptop.

- Podman couldn't unpack image layers, so the operator image for Day 5 Part 6 was built with [ko](https://ko.build/) instead of `make docker-build`:
  `KO_DOCKER_REPO=localhost/site-operator ko build ./cmd --bare --tags dev --platform linux/arm64 --push=false --tarball site-operator.tar`, then imported into the node and the manager Deployment's command set to `/ko-app/cmd`.
- Long-running commands (`make run`, `make test`) were started with `setsid nohup … & disown` so they survived the web terminal closing.
