/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

const defaultKVImage = "valkey/valkey:8.0"

// KVClusterReconciler reconciles a KVCluster object
type KVClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=web.example.com,resources=kvclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.example.com,resources=kvclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.example.com,resources=kvclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func headlessName(c *webv1alpha1.KVCluster) string { return c.Name + "-headless" }
func primaryName(c *webv1alpha1.KVCluster) string  { return c.Name + "-0" }

// startScript: member 0 is the primary; everyone else copies from it.
func startScript(c *webv1alpha1.KVCluster) string {
	return fmt.Sprintf(`ORDINAL=${HOSTNAME##*-}
if [ "$ORDINAL" = "0" ]; then
  exec valkey-server --appendonly yes --dir /data
fi
exec valkey-server --appendonly yes --dir /data --replicaof %s.%s 6379
`, primaryName(c), headlessName(c))
}

// Reconcile builds the headless Service, the client Service and the StatefulSet,
// grows disks when asked, and reports back.
func (r *KVClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var c webv1alpha1.KVCluster
	if err := r.Get(ctx, req.NamespacedName, &c); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	prev := c.Status.State
	labels := map[string]string{"app": c.Name, "component": "kv"}

	// 1. Direct phone numbers: the headless Service.
	headless := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: headlessName(&c), Namespace: c.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, headless, func() error {
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.PublishNotReadyAddresses = true
		headless.Spec.Selector = labels
		headless.Spec.Ports = []corev1.ServicePort{{Name: "valkey", Port: 6379, TargetPort: intstr.FromInt32(6379), Protocol: corev1.ProtocolTCP}}
		return controllerutil.SetControllerReference(&c, headless, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	// 2. The front door for writes: a Service that selects the primary Pod only.
	clientSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: c.Name, Namespace: c.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, clientSvc, func() error {
		clientSvc.Spec.Selector = map[string]string{
			"app": c.Name, "component": "kv",
			"statefulset.kubernetes.io/pod-name": primaryName(&c), // label the StatefulSet puts on each Pod
		}
		clientSvc.Spec.Ports = []corev1.ServicePort{{Name: "valkey", Port: 6379, TargetPort: intstr.FromInt32(6379), Protocol: corev1.ProtocolTCP}}
		return controllerutil.SetControllerReference(&c, clientSvc, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	// 3. The members: the StatefulSet.
	script := startScript(&c)
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: c.Name, Namespace: c.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Spec.ServiceName = headlessName(&c)
		sts.Spec.Replicas = ptr.To(c.Spec.Replicas)
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		sts.Spec.Template.Labels = labels
		if sts.Spec.Template.Annotations == nil {
			sts.Spec.Template.Annotations = map[string]string{}
		}
		sts.Spec.Template.Annotations["web.example.com/script-hash"] = contentHash(script)

		pod := &sts.Spec.Template.Spec
		if len(pod.Containers) == 0 {
			pod.Containers = []corev1.Container{{Name: "valkey"}}
		}
		k := &pod.Containers[0]
		k.Image = imageOr(c.Spec.Image, defaultKVImage)
		k.Command = []string{"sh", "-c", script}
		k.Ports = []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
		k.VolumeMounts = []corev1.VolumeMount{{Name: "data", MountPath: "/data"}}
		k.ReadinessProbe = &corev1.Probe{
			ProbeHandler:  corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"valkey-cli", "ping"}}},
			PeriodSeconds: 5, TimeoutSeconds: 1, SuccessThreshold: 1, FailureThreshold: 3,
		}

		// The locker request form can only be written once: Kubernetes forbids changing it.
		if len(sts.Spec.VolumeClaimTemplates) == 0 {
			sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "data"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: c.Spec.StorageClassName,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: c.Spec.Storage},
					},
				},
			}}
		}
		return controllerutil.SetControllerReference(&c, sts, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Grow disks if asked (volumeClaimTemplates can't change, so edit each PVC).
	if err := r.growDisks(ctx, &c); err != nil {
		return ctrl.Result{}, err
	}

	// 5. Report back.
	c.Status.ReadyReplicas = sts.Status.ReadyReplicas
	c.Status.Primary = primaryName(&c)
	progress := fmt.Sprintf("%d/%d members ready", sts.Status.ReadyReplicas, c.Spec.Replicas)
	if sts.Status.ObservedGeneration == sts.Generation &&
		sts.Status.UpdatedReplicas == c.Spec.Replicas &&
		sts.Status.ReadyReplicas == c.Spec.Replicas &&
		sts.Status.CurrentRevision == sts.Status.UpdateRevision {
		return r.report(ctx, &c, prev, "ready", "AllMembersReady", progress)
	}
	return r.report(ctx, &c, prev, "initializing", "MembersStarting", progress)
}

// growDisks raises each member's PVC to the requested size.
func (r *KVClusterReconciler) growDisks(ctx context.Context, c *webv1alpha1.KVCluster) error {
	for i := int32(0); i < c.Spec.Replicas; i++ {
		var pvc corev1.PersistentVolumeClaim
		key := types.NamespacedName{Namespace: c.Namespace, Name: fmt.Sprintf("data-%s-%d", c.Name, i)}
		if err := r.Get(ctx, key, &pvc); err != nil {
			if apierrors.IsNotFound(err) {
				continue // not created yet
			}
			return err
		}
		current := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if current.Cmp(c.Spec.Storage) < 0 {
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = c.Spec.Storage
			if err := r.Update(ctx, &pvc); err != nil {
				return fmt.Errorf("growing %s: %w", pvc.Name, err)
			}
		}
	}
	return nil
}

// setKVCond adds or replaces one condition on the KVCluster.
func setKVCond(c *webv1alpha1.KVCluster, condType string, ok bool, reason, msg string) {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
		Type: condType, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: c.Generation,
	})
}

// report writes the summary state, the Ready condition and, on change, an event.
func (r *KVClusterReconciler) report(ctx context.Context, c *webv1alpha1.KVCluster,
	prev, state, reason, msg string) (ctrl.Result, error) {
	c.Status.State = state
	c.Status.ObservedGeneration = c.Generation
	setKVCond(c, "Ready", state == "ready", reason, msg)
	if state != prev {
		r.Recorder.Eventf(c, nil, corev1.EventTypeNormal, reason, "Reconcile", "State is now %s: %s", state, msg)
	}
	return ctrl.Result{}, r.Status().Update(ctx, c)
}

// SetupWithManager sets up the controller with the Manager.
func (r *KVClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1alpha1.KVCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Named("kvcluster").
		Complete(r)
}
