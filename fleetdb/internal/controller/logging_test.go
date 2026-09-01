package controller

import (
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func reconcileWithLogBuffer(name string) (string, error) {
	var buf bytes.Buffer
	logger := zap.New(zap.WriteTo(&buf))
	ctx := log.IntoContext(context.Background(), logger)

	r := &PostgresTenantReconciler{
		Client:   k8sClient,
		Scheme:   scheme.Scheme,
		Recorder: record.NewFakeRecorder(32),
	}
	req := ctrl.Request{
		NamespacedName: namespaceName(name, "default"),
	}
	_, err := r.Reconcile(ctx, req)
	return buf.String(), err
}

var _ = Describe("FleetDB logging", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond

	It("keeps per-reconcile chatter out of the info level", func() {
		tenant := createTenant("rulers", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())
		var logs string
		Eventually(func() error {
			var err error
			logs, err = reconcileWithLogBuffer("rulers")
			return err
		}, timeout, interval).Should(Succeed())
		Expect(logs).NotTo(ContainSubstring("starting reconciliation"))
		Expect(logs).NotTo(ContainSubstring("ObjectMeta{"))
	})
	It("never logs the generated password", func() {
		tenant := createTenant("gaskets", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		var logs string
		Eventually(func() error {
			var err error
			logs, err = reconcileWithLogBuffer("gaskets")
			return err
		}, timeout, interval).Should(Succeed())

		var secret corev1.Secret
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: "gaskets-postgres", Namespace: "default",
			}, &secret)
		}, timeout, interval).Should(Succeed())

		Expect(logs).NotTo(ContainSubstring(
			string(secret.Data["POSTGRES_PASSWORD"])))
	})
})
