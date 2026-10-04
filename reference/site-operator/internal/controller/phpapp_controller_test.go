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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

var _ = Describe("PhpApp Controller", func() {
	It("builds the kitchen but waits before the front counter", func() {
		ctx := context.Background()

		By("creating a Secret and a PhpApp")
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "t1-secrets", Namespace: "default"},
			StringData: map[string]string{"GREETING": "hi"},
		})).To(Succeed())
		app := &webv1alpha1.PhpApp{
			ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "default"},
			Spec: webv1alpha1.PhpAppSpec{
				Code: "<?php echo 1;", SecretName: "t1-secrets",
				Php: webv1alpha1.ComponentSpec{Replicas: 1}, Nginx: webv1alpha1.ComponentSpec{Replicas: 1},
			},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		By("running one Reconcile pass")
		r := &PhpAppReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: events.NewFakeRecorder(100)}
		key := types.NamespacedName{Name: "t1", Namespace: "default"}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		By("finding the php Deployment, owned by the PhpApp")
		var php appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "t1-php", Namespace: "default"}, &php)).To(Succeed())
		Expect(php.OwnerReferences).To(HaveLen(1))
		Expect(php.OwnerReferences[0].Kind).To(Equal("PhpApp"))

		By("not building nginx yet, because no php Pod is ready")
		var nginx appsv1.Deployment
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "t1-nginx", Namespace: "default"}, &nginx)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		By("reporting initializing, with our finalizer on")
		Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
		Expect(app.Status.State).To(Equal("initializing"))
		Expect(app.Finalizers).To(ContainElement(finalizerName))
	})

	It("reports a missing Secret instead of failing", func() {
		ctx := context.Background()
		app := &webv1alpha1.PhpApp{
			ObjectMeta: metav1.ObjectMeta{Name: "t2", Namespace: "default"},
			Spec:       webv1alpha1.PhpAppSpec{Code: "<?php echo 2;", SecretName: "nope"},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		r := &PhpAppReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: events.NewFakeRecorder(100)}
		key := types.NamespacedName{Name: "t2", Namespace: "default"}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred()) // waiting is not an error

		Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
		Expect(app.Status.State).To(Equal("error"))
		Expect(meta.IsStatusConditionFalse(app.Status.Conditions, "SecretFound")).To(BeTrue())
	})

	It("rejects code without a <?php tag (CEL rule)", func() {
		ctx := context.Background()
		app := &webv1alpha1.PhpApp{
			ObjectMeta: metav1.ObjectMeta{Name: "t3", Namespace: "default"},
			Spec:       webv1alpha1.PhpAppSpec{Code: "hello", SecretName: "x"},
		}
		err := k8sClient.Create(ctx, app)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("code must contain an opening <?php tag"))
	})
})
