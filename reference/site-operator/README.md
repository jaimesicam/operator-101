# Reference code: site-operator

The finished operator from the course, with all four kinds in one Kubebuilder project:

| Kind | API version | Built on | Controller |
| --- | --- | --- | --- |
| StaticSite | `web.example.com/v1alpha1` | Day 2–3 | [`staticsite_controller.go`](internal/controller/staticsite_controller.go) |
| PhpApp | `v1alpha1`, `v1beta1` (storage) | Day 4–5 | [`phpapp_controller.go`](internal/controller/phpapp_controller.go) |
| KVCluster | `v1alpha1` | Day 6 | [`kvcluster_controller.go`](internal/controller/kvcluster_controller.go) |
| KVBackup | `v1alpha1` | Day 6 | [`kvbackup_controller.go`](internal/controller/kvbackup_controller.go) |

Only the files you write during the course are kept here: the API types, the controllers and their envtest tests. Everything else (Makefile, `config/`, generated deepcopy code, CRD YAML) comes from the Kubebuilder scaffold, so this folder stays short and readable.

## Rebuild the full project

```bash
./setup.sh                 # creates ./site-operator-build
cd site-operator-build
make install               # CRDs into your current cluster
make run                   # run the operator from your laptop
```

`setup.sh` runs the same `kubebuilder init` / `create api` commands as the lessons, copies these files over the scaffold, wires an event recorder into `cmd/main.go`, and runs `make manifests generate fmt vet test`. You need Go, Kubebuilder v4 and python3.

To run it inside the cluster (Day 5, Part 6):

```bash
make docker-build IMG=site-operator:dev
kind load docker-image site-operator:dev --name ops-lab      # or: k3d image import site-operator:dev -c ops-lab
make deploy IMG=site-operator:dev
```

## What the tests cover

`make test` starts envtest (a real API server and etcd, no kubelet) and checks:

- StaticSite builds a ConfigMap, Deployment and Service owned by the StaticSite, applies the `nginx:stable` default and reports `initializing`
- PhpApp builds the php-fpm Deployment but waits before nginx (Gate 2), and adds its finalizer
- PhpApp reports a missing Secret as `error` with `SecretFound=False`, without returning an error
- The PhpApp CEL rule rejects code without `<?php`
- KVCluster builds a headless Service, a primary-only Service and a StatefulSet with a disk template
- The KVCluster CEL transition rule refuses to shrink storage
- A KVBackup for a missing cluster ends `Failed` and is never run again

## Differences from the lesson text

- Events use the newer `events.k8s.io` API (`k8s.io/client-go/tools/events`), as the Day 3 lesson explains. `KVBackup` doesn't emit events.
- StaticSite's "Created …" events go through a small `noteCreated` helper that passes the child as the *related* object.
- `api/v1beta1/phpapp_types.go` is the Day 5 Part 7 copy marked `+kubebuilder:storageversion`.
