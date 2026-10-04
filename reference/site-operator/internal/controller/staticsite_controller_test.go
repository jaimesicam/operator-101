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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

var _ = Describe("StaticSite Controller", func() {
	It("builds a ConfigMap, Deployment and Service owned by the StaticSite", func() {
		ctx := context.Background()
		site := &webv1alpha1.StaticSite{
			ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "default"},
			Spec:       webv1alpha1.StaticSiteSpec{Content: "<h1>hi</h1>", Replicas: 2},
		}
		Expect(k8sClient.Create(ctx, site)).To(Succeed())

		r := &StaticSiteReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: events.NewFakeRecorder(100)}
		key := types.NamespacedName{Name: "s1", Namespace: "default"}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, key, &cm)).To(Succeed())
		Expect(cm.Data["index.html"]).To(Equal("<h1>hi</h1>"))

		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, key, &dep)).To(Succeed())
		Expect(*dep.Spec.Replicas).To(Equal(int32(2)))
		Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal("nginx:stable")) // schema default
		Expect(dep.OwnerReferences[0].Kind).To(Equal("StaticSite"))

		var svc corev1.Service
		Expect(k8sClient.Get(ctx, key, &svc)).To(Succeed())

		By("reporting initializing: envtest has no kubelet, so no Pod ever becomes ready")
		Expect(k8sClient.Get(ctx, key, site)).To(Succeed())
		Expect(site.Status.State).To(Equal("initializing"))
		Expect(site.Status.ObservedGeneration).To(Equal(site.Generation))
	})
})
