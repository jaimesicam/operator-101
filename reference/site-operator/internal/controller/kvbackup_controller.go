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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

// KVBackupReconciler reconciles a KVBackup object
type KVBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=web.example.com,resources=kvbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.example.com,resources=kvbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.example.com,resources=kvbackups/finalizers,verbs=update
// +kubebuilder:rbac:groups=web.example.com,resources=kvclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch

// Reconcile takes one backup with a Job, then records the result for good.
func (r *KVBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var b webv1alpha1.KVBackup
	if err := r.Get(ctx, req.NamespacedName, &b); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// A finished backup is history. Never run it again.
	if b.Status.Phase == "Succeeded" || b.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}

	// The cluster must exist and be healthy.
	var c webv1alpha1.KVCluster
	if err := r.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Spec.Cluster}, &c); err != nil {
		if apierrors.IsNotFound(err) {
			b.Status.Phase = "Failed"
			return ctrl.Result{}, r.Status().Update(ctx, &b)
		}
		return ctrl.Result{}, err
	}
	if c.Status.State != "ready" {
		b.Status.Phase = "Waiting"
		// We don't watch KVClusters from here, so check again on a timer.
		return ctrl.Result{RequeueAfter: 15 * time.Second}, r.Status().Update(ctx, &b)
	}

	// A disk for the backup file, owned by the KVBackup.
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		if pvc.CreationTimestamp.IsZero() { // most of a PVC's spec is fixed after creation
			pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
			pvc.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: c.Spec.Storage}
		}
		return controllerutil.SetControllerReference(&b, pvc, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	// The Job: one Pod that copies a snapshot from the primary into /backup.
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		if job.CreationTimestamp.IsZero() { // a Job's Pod template is fixed after creation
			job.Spec.BackoffLimit = ptr.To[int32](2)
			job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
			job.Spec.Template.Spec.Containers = []corev1.Container{{
				Name:  "backup",
				Image: imageOr(c.Spec.Image, defaultKVImage),
				Command: []string{"valkey-cli", "-h", primaryName(&c) + "." + headlessName(&c),
					"--rdb", "/backup/dump.rdb"},
				VolumeMounts: []corev1.VolumeMount{{Name: "backup", MountPath: "/backup"}},
			}}
			job.Spec.Template.Spec.Volumes = []corev1.Volume{{
				Name:         "backup",
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc.Name}},
			}}
		}
		return controllerutil.SetControllerReference(&b, job, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	// Report the phase from the Job's report card.
	switch {
	case job.Status.Succeeded > 0:
		b.Status.Phase = "Succeeded"
		b.Status.Location = "pvc/" + pvc.Name + ":/dump.rdb"
		b.Status.CompletedAt = job.Status.CompletionTime
	case jobFailed(job):
		b.Status.Phase = "Failed"
	default:
		b.Status.Phase = "Running"
	}
	return ctrl.Result{}, r.Status().Update(ctx, &b)
}

func jobFailed(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *KVBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1alpha1.KVBackup{}).
		Owns(&batchv1.Job{}).
		Named("kvbackup").
		Complete(r)
}
