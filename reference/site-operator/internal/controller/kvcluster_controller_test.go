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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

var _ = Describe("KVCluster Controller", func() {
	ctx := context.Background()

	It("builds two Services and a StatefulSet with a disk template", func() {
		c := &webv1alpha1.KVCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "kv1", Namespace: "default"},
			Spec:       webv1alpha1.KVClusterSpec{Replicas: 3, Storage: resource.MustParse("1Gi")},
		}
		Expect(k8sClient.Create(ctx, c)).To(Succeed())

		r := &KVClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: events.NewFakeRecorder(100)}
		key := types.NamespacedName{Name: "kv1", Namespace: "default"}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var headless corev1.Service
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kv1-headless", Namespace: "default"}, &headless)).To(Succeed())
		Expect(headless.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))

		var front corev1.Service
		Expect(k8sClient.Get(ctx, key, &front)).To(Succeed())
		Expect(front.Spec.Selector).To(HaveKeyWithValue("statefulset.kubernetes.io/pod-name", "kv1-0"))

		var sts appsv1.StatefulSet
		Expect(k8sClient.Get(ctx, key, &sts)).To(Succeed())
		Expect(sts.Spec.ServiceName).To(Equal("kv1-headless"))
		Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))

		Expect(k8sClient.Get(ctx, key, c)).To(Succeed())
		Expect(c.Status.State).To(Equal("initializing"))
		Expect(c.Status.Primary).To(Equal("kv1-0"))
	})

	It("refuses to shrink storage (CEL transition rule)", func() {
		var c webv1alpha1.KVCluster
		key := types.NamespacedName{Name: "kv1", Namespace: "default"}
		Expect(k8sClient.Get(ctx, key, &c)).To(Succeed())
		c.Spec.Storage = resource.MustParse("500Mi")
		err := k8sClient.Update(ctx, &c)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("storage can only grow"))
	})
})
