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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

const (
	defaultPhpImage   = "php:8.3-fpm"
	defaultNginxImage = "nginx:stable"
	codeDir           = "/var/www/html" // where php-fpm finds index.php

	finalizerName = "web.example.com/directory-cleanup"
	directoryNS   = "directory"
	directoryName = "phpapps"
)

// PhpAppReconciler reconciles a PhpApp object
type PhpAppReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=web.example.com,resources=phpapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.example.com,resources=phpapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.example.com,resources=phpapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile builds the kitchen (php-fpm) and the front counter (nginx), in order, behind gates.
func (r *PhpAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var app webv1alpha1.PhpApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Being deleted? Do the last job, then let go.
	if !app.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&app, finalizerName) {
			if err := r.removeFromDirectory(ctx, &app); err != nil {
				return ctrl.Result{}, err // retry; the PhpApp waits in Terminating
			}
			controllerutil.RemoveFinalizer(&app, finalizerName)
			if err := r.Update(ctx, &app); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Alive: make sure our sticky note is on before we create anything outside.
	if controllerutil.AddFinalizer(&app, finalizerName) {
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
	}

	prev := app.Status.State

	// Gate 1: is the secret ingredient in the cupboard?
	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Spec.SecretName}, &secret)
	if apierrors.IsNotFound(err) {
		msg := fmt.Sprintf("Secret %q does not exist", app.Spec.SecretName)
		setCond(&app, "SecretFound", false, "SecretMissing", msg)
		return r.report(ctx, &app, prev, "error", "SecretMissing", msg)
	}
	if err != nil {
		return ctrl.Result{}, err // a real error: retry with backoff
	}
	setCond(&app, "SecretFound", true, "SecretFound", "")

	// Step 2: build the kitchen.
	php, err := r.ensurePhp(ctx, &app, &secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	if isStuck(php) {
		return r.report(ctx, &app, prev, "error", "PhpRolloutStuck", "php-fpm Pods are not becoming ready")
	}

	// Gate 2: at least one cook ready before opening the front door.
	if php.Status.ReadyReplicas == 0 {
		setCond(&app, "PhpReady", false, "Starting", "no php-fpm Pod is ready yet")
		return r.report(ctx, &app, prev, "initializing", "PhpStarting", "waiting for php-fpm")
	}
	setCond(&app, "PhpReady", true, "PodsReady", fmt.Sprintf("%d php-fpm Pods ready", php.Status.ReadyReplicas))

	// Step 3: build the front counter.
	nginx, err := r.ensureNginx(ctx, &app)
	if err != nil {
		return ctrl.Result{}, err
	}
	if isStuck(nginx) {
		return r.report(ctx, &app, prev, "error", "NginxRolloutStuck", "nginx Pods are not becoming ready")
	}

	// Gate 3: is everything fully rolled out?
	if !rolledOut(php, app.Spec.Php.Replicas) || !rolledOut(nginx, app.Spec.Nginx.Replicas) {
		return r.report(ctx, &app, prev, "initializing", "RollingOut", "waiting for all Pods to be updated and ready")
	}

	if err := r.addToDirectory(ctx, &app); err != nil {
		return ctrl.Result{}, err
	}
	return r.report(ctx, &app, prev, "ready", "AllReady", "nginx and php-fpm are ready")
}

// ---------- shared helpers ----------

func labelsFor(app *webv1alpha1.PhpApp, component string) map[string]string {
	return map[string]string{"app": app.Name, "component": component}
}

func imageOr(image, fallback string) string {
	if image == "" {
		return fallback
	}
	return image
}

// tcpProbe checks that something is listening on a port.
// Every default is spelled out so CreateOrUpdate sees no difference (Day 3, Part 6).
func tcpProbe(port int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)}},
		PeriodSeconds:    5,
		TimeoutSeconds:   1,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}
}

// setCond adds or replaces one condition on the PhpApp.
func setCond(app *webv1alpha1.PhpApp, condType string, ok bool, reason, msg string) {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type: condType, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: app.Generation,
	})
}

// rolledOut: the Deployment has seen its latest spec and every Pod is updated and ready.
func rolledOut(dep *appsv1.Deployment, want int32) bool {
	return dep.Status.ObservedGeneration == dep.Generation &&
		dep.Status.UpdatedReplicas == want &&
		dep.Status.ReadyReplicas == want
}

// report writes the summary state, the Ready condition and, on change, an event.
func (r *PhpAppReconciler) report(ctx context.Context, app *webv1alpha1.PhpApp,
	prev, state, reason, msg string) (ctrl.Result, error) {
	app.Status.State = state
	app.Status.ObservedGeneration = app.Generation
	setCond(app, "Ready", state == "ready", reason, msg)
	if state != prev {
		eventType := corev1.EventTypeNormal
		if state == "error" {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(app, nil, eventType, reason, "Reconcile", "State is now %s: %s", state, msg)
	}
	return ctrl.Result{}, r.Status().Update(ctx, app)
}

// ---------- the kitchen ----------

// ensurePhp builds the kitchen: code ConfigMap, php-fpm Deployment and Service.
func (r *PhpAppReconciler) ensurePhp(ctx context.Context, app *webv1alpha1.PhpApp, secret *corev1.Secret) (*appsv1.Deployment, error) {
	// The recipe.
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-code", Namespace: app.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = map[string]string{"app": app.Name}
		cm.Data = map[string]string{"index.php": app.Spec.Code}
		return controllerutil.SetControllerReference(app, cm, r.Scheme)
	}); err != nil {
		return nil, err
	}

	// The cooks.
	labels := labelsFor(app, "php")
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-php", Namespace: app.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec.Replicas = ptr.To(app.Spec.Php.Replicas)
		dep.Spec.ProgressDeadlineSeconds = ptr.To[int32](60)
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template.Labels = labels

		// Restart the cooks when the recipe or the secret ingredient changes.
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = map[string]string{}
		}
		dep.Spec.Template.Annotations["web.example.com/code-hash"] = contentHash(app.Spec.Code)
		dep.Spec.Template.Annotations["web.example.com/secret-version"] = secret.ResourceVersion

		pod := &dep.Spec.Template.Spec
		if len(pod.Containers) == 0 {
			pod.Containers = []corev1.Container{{Name: "php-fpm"}}
		}
		c := &pod.Containers[0]
		c.Image = imageOr(app.Spec.Php.Image, defaultPhpImage)
		c.Ports = []corev1.ContainerPort{{Name: "fastcgi", ContainerPort: 9000, Protocol: corev1.ProtocolTCP}}
		c.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
		}}}
		c.VolumeMounts = []corev1.VolumeMount{{Name: "code", MountPath: codeDir}}
		c.ReadinessProbe = tcpProbe(9000)

		pod.Volumes = []corev1.Volume{{
			Name: "code",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name},
				DefaultMode:          ptr.To[int32](0o644),
			}},
		}}
		return controllerutil.SetControllerReference(app, dep, r.Scheme)
	}); err != nil {
		return nil, err
	}

	// The hatch.
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-php", Namespace: app.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = map[string]string{"app": app.Name}
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name: "fastcgi", Port: 9000, TargetPort: intstr.FromInt32(9000), Protocol: corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(app, svc, r.Scheme)
	}); err != nil {
		return nil, err
	}
	return dep, nil
}

// ---------- the front counter ----------

// nginxConfig is the waiter's instruction card.
func nginxConfig(app *webv1alpha1.PhpApp) string {
	return fmt.Sprintf(`server {
    listen 80;
    location / {
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME %s/index.php;
        fastcgi_pass %s-php:9000;
    }
}
`, codeDir, app.Name)
}

// ensureNginx builds the front counter: config ConfigMap, nginx Deployment and Service.
func (r *PhpAppReconciler) ensureNginx(ctx context.Context, app *webv1alpha1.PhpApp) (*appsv1.Deployment, error) {
	conf := nginxConfig(app)

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-nginx", Namespace: app.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = map[string]string{"app": app.Name}
		cm.Data = map[string]string{"default.conf": conf}
		return controllerutil.SetControllerReference(app, cm, r.Scheme)
	}); err != nil {
		return nil, err
	}

	labels := labelsFor(app, "nginx")
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-nginx", Namespace: app.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec.Replicas = ptr.To(app.Spec.Nginx.Replicas)
		dep.Spec.ProgressDeadlineSeconds = ptr.To[int32](60)
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template.Labels = labels

		// nginx never re-reads its config by itself: restart it when the card changes.
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = map[string]string{}
		}
		dep.Spec.Template.Annotations["web.example.com/config-hash"] = contentHash(conf)

		pod := &dep.Spec.Template.Spec
		if len(pod.Containers) == 0 {
			pod.Containers = []corev1.Container{{Name: "nginx"}}
		}
		c := &pod.Containers[0]
		c.Image = imageOr(app.Spec.Nginx.Image, defaultNginxImage)
		c.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: 80, Protocol: corev1.ProtocolTCP}}
		c.VolumeMounts = []corev1.VolumeMount{{Name: "conf", MountPath: "/etc/nginx/conf.d"}}
		c.ReadinessProbe = tcpProbe(80)

		pod.Volumes = []corev1.Volume{{
			Name: "conf",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name},
				DefaultMode:          ptr.To[int32](0o644),
			}},
		}}
		return controllerutil.SetControllerReference(app, dep, r.Scheme)
	}); err != nil {
		return nil, err
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: app.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = map[string]string{"app": app.Name}
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(80), Protocol: corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(app, svc, r.Scheme)
	}); err != nil {
		return nil, err
	}
	return dep, nil
}

// ---------- the shop directory (Day 5 finalizer) ----------

func directoryKey(app *webv1alpha1.PhpApp) string { return app.Namespace + "." + app.Name }

// addToDirectory writes (or refreshes) this app's entry.
func (r *PhpAppReconciler) addToDirectory(ctx context.Context, app *webv1alpha1.PhpApp) error {
	dir := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: directoryName, Namespace: directoryNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dir, func() error {
		if dir.Data == nil {
			dir.Data = map[string]string{}
		}
		dir.Data[directoryKey(app)] = fmt.Sprintf("http://%s.%s.svc", app.Name, app.Namespace)
		return nil
	})
	return err
}

// removeFromDirectory deletes this app's entry. Missing directory = nothing to do.
func (r *PhpAppReconciler) removeFromDirectory(ctx context.Context, app *webv1alpha1.PhpApp) error {
	var dir corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{Namespace: directoryNS, Name: directoryName}, &dir)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	delete(dir.Data, directoryKey(app))
	return r.Update(ctx, &dir)
}

// ---------- watching the Secret ----------

// appsUsingSecret maps a changed Secret to the PhpApps that use it.
func (r *PhpAppReconciler) appsUsingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	var apps webv1alpha1.PhpAppList
	if err := r.List(ctx, &apps, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, app := range apps.Items {
		if app.Spec.SecretName == obj.GetName() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: app.Namespace, Name: app.Name},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *PhpAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1alpha1.PhpApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.appsUsingSecret)).
		Named("phpapp").
		Complete(r)
}
