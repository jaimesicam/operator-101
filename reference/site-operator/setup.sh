#!/usr/bin/env bash
# Rebuild the finished site-operator project from a fresh Kubebuilder scaffold.
#
# Usage:  ./setup.sh [target-dir]        (default: ./site-operator-build)
#
# Needs: Go (the version your Kubebuilder release asks for), kubebuilder v4, make, python3.
# Tested with Kubebuilder v4.16.0, Go 1.26.0, controller-runtime v0.25.0.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST="${1:-site-operator-build}"

if [ -e "$DEST" ]; then
  echo "error: $DEST already exists; pick another directory" >&2
  exit 1
fi
mkdir -p "$DEST"
cd "$DEST"

echo "==> Scaffolding the project"
kubebuilder init --domain example.com --repo example.com/site-operator
for kind in StaticSite PhpApp KVCluster KVBackup; do
  kubebuilder create api --group web --version v1alpha1 --kind "$kind" --resource --controller
done
# Day 5, Part 7: a second, same-shaped version that becomes the storage version.
kubebuilder create api --group web --version v1beta1 --kind PhpApp --resource --controller=false

echo "==> Copying the finished types, controllers and tests"
cp "$HERE"/api/v1alpha1/*_types.go api/v1alpha1/
cp "$HERE"/api/v1beta1/*_types.go api/v1beta1/
cp "$HERE"/internal/controller/*.go internal/controller/

echo "==> Wiring an event recorder into cmd/main.go (Day 3, Part 4)"
python3 - <<'PY'
import re, sys
p = "cmd/main.go"
src = open(p).read()
for kind, name in [("StaticSite", "staticsite"), ("PhpApp", "phpapp"), ("KVCluster", "kvcluster")]:
    pat = re.compile(r"(controller\.%sReconciler\{\s*Client:\s*mgr\.GetClient\(\),\s*Scheme:\s*mgr\.GetScheme\(\),\n)" % kind)
    src, n = pat.subn(r'\1\t\tRecorder: mgr.GetEventRecorder("%s-controller"),\n' % name, src)
    if n != 1:
        sys.exit("could not find the %sReconciler block in cmd/main.go" % kind)
open(p, "w").write(src)
PY

echo "==> Generating code and manifests, then running the envtest suite"
make manifests generate fmt vet
make test

cat <<EOF

Done. The project is in: $(pwd)

Next steps (see the Day 2 and Day 5 lessons):
  make install            # CRDs into your current cluster
  make run                # run the operator from your laptop
EOF
