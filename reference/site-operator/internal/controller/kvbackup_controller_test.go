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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	webv1alpha1 "example.com/site-operator/api/v1alpha1"
)

var _ = Describe("KVBackup Controller", func() {
	It("fails a backup whose cluster does not exist, and never runs it again", func() {
		ctx := context.Background()
		b := &webv1alpha1.KVBackup{
			ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "default"},
			Spec:       webv1alpha1.KVBackupSpec{Cluster: "no-such-cluster"},
		}
		Expect(k8sClient.Create(ctx, b)).To(Succeed())

		r := &KVBackupReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		key := types.NamespacedName{Name: "b1", Namespace: "default"}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, key, b)).To(Succeed())
		Expect(b.Status.Phase).To(Equal("Failed"))

		By("a second pass leaves a finished backup alone")
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, key, b)).To(Succeed())
		Expect(b.Status.Phase).To(Equal("Failed"))
	})
})
