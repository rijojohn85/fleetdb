package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	postgresv1alpha1 "github.com/rijojohn85/fleetdb/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var (
	timeout  = time.Second * 30
	interval = time.Millisecond * 500
)

var _ = Describe("PostgresTenant reconciller tests", func() {
	It("creates a secret holding the tenant's db creds", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "acme",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "acme",
				StorageSize:  mustQuantity("1Gi"),
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		secret := &corev1.Secret{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: "default",
				Name:      "acme-postgres",
			}, secret)
		}, timeout, interval).Should(Succeed())
		Expect(secret.Data).To(HaveKey("POSTGRES_USER"))
		Expect(secret.Data).To(HaveKey("POSTGRES_PASSWORD"))
		Expect(secret.Data["POSTGRES_DB"]).To(Equal([]byte("acme")))
	})
})
