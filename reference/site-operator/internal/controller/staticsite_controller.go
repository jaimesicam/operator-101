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
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

// StaticSiteReconciler reconciles a StaticSite object
type StaticSiteReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=web.example.com,resources=staticsites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.example.com,resources=staticsites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.example.com,resources=staticsites/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile makes the ConfigMap, Deployment and Service match the StaticSite,
// then reports back in status.
func (r *StaticSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconcile start")

	// 1. Read the wish. If the StaticSite is gone, there's nothing to do.
	var site webv1alpha1.StaticSite
	if err := r.Get(ctx, req.NamespacedName, &site); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	prevState := site.Status.State

	labels := map[string]string{"app": site.Name}

	// 2. The note card (ConfigMap).
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: site.Name, Namespace: site.Namespace}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{"index.html": site.Spec.Content}
		return controllerutil.SetControllerReference(&site, cm, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("configmap", "result", op)
	r.noteCreated(&site, op, "ConfigMap", cm)

	// 3. The recipe card (Deployment). Only the fields the StaticSite decides are set;
	// defaults the API server fills in are left alone, so quiet passes report "unchanged".
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: site.Name, Namespace: site.Namespace}}
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec.Replicas = ptr.To(site.Spec.Replicas)
		dep.Spec.ProgressDeadlineSeconds = ptr.To[int32](60)
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template.Labels = labels

		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = map[string]string{}
		}
		dep.Spec.Template.Annotations["web.example.com/content-hash"] = contentHash(site.Spec.Content)

		pod := &dep.Spec.Template.Spec
		if len(pod.Containers) == 0 {
			pod.Containers = []corev1.Container{{Name: "nginx"}}
		}
		c := &pod.Containers[0] // edit the existing container in place
		c.Image = site.Spec.Image
		c.Ports = []corev1.ContainerPort{{ContainerPort: 80, Protocol: corev1.ProtocolTCP}}
		c.VolumeMounts = []corev1.VolumeMount{{Name: "html", MountPath: "/usr/share/nginx/html"}}

		pod.Volumes = []corev1.Volume{{
			Name: "html",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: site.Name},
				DefaultMode:          ptr.To[int32](0o644), // the default, spelled out
			}},
		}}
		return controllerutil.SetControllerReference(&site, dep, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("deployment", "result", op)
	r.noteCreated(&site, op, "Deployment", dep)

	// 4. The phone number (Service).
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: site.Name, Namespace: site.Namespace}}
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Port: 80, TargetPort: intstr.FromInt32(80), Protocol: corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(&site, svc, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("service", "result", op)
	r.noteCreated(&site, op, "Service", svc)

	// 5. Report back: summarize the Deployment's report card.
	site.Status.ReadyReplicas = dep.Status.ReadyReplicas
	site.Status.ObservedGeneration = site.Generation
	ready := metav1.Condition{Type: "Ready", ObservedGeneration: site.Generation}
	progress := fmt.Sprintf("%d/%d replicas ready", dep.Status.ReadyReplicas, site.Spec.Replicas)

	switch {
	case isStuck(dep):
		site.Status.State = "error"
		ready.Status, ready.Reason = metav1.ConditionFalse, "RolloutStuck"
		ready.Message = "nginx Pods are not becoming ready; check the image and Pod events"
	case dep.Status.ObservedGeneration == dep.Generation &&
		dep.Status.UpdatedReplicas == site.Spec.Replicas &&
		dep.Status.ReadyReplicas == site.Spec.Replicas:
		site.Status.State = "ready"
		ready.Status, ready.Reason, ready.Message = metav1.ConditionTrue, "AllReplicasReady", progress
	default:
		site.Status.State = "initializing"
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, "RollingOut", progress
	}
	meta.SetStatusCondition(&site.Status.Conditions, ready)

	if site.Status.State != prevState {
		eventType := corev1.EventTypeNormal
		if site.Status.State == "error" {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(&site, nil, eventType, ready.Reason, "Reconcile",
			"State is now %s: %s", site.Status.State, ready.Message)
	}

	if err := r.Status().Update(ctx, &site); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// noteCreated leaves a Normal event when a child object was just created.
// The child goes in the "related" slot: events with the same regarding object,
// reason and action are merged into one series, so related keeps them apart.
func (r *StaticSiteReconciler) noteCreated(site *webv1alpha1.StaticSite, op controllerutil.OperationResult, kind string, child client.Object) {
	if op == controllerutil.OperationResultCreated {
		r.Recorder.Eventf(site, child, corev1.EventTypeNormal, "Created", "Create", "Created %s %s", kind, child.GetName())
	}
}

// contentHash is a short fingerprint of a piece of text.
func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// isStuck reports whether the Deployment gave up on its rollout.
func isStuck(dep *appsv1.Deployment) bool {
	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing &&
			c.Status == corev1.ConditionFalse &&
			c.Reason == "ProgressDeadlineExceeded" {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *StaticSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1alpha1.StaticSite{}). // wake up when a StaticSite changes
		Owns(&corev1.ConfigMap{}).      // ...or when a child it owns changes
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("staticsite").
		Complete(r)
}
