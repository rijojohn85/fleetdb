package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	postgresv1alpha1 "github.com/rijojohn85/fleetdb/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("PostgresTenant API validation", func() {
	res := resource.MustParse("1Gi")
	It("rejects a PostgresTenant with no Database name", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-db-name",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				StorageSize: &res,
			},
		}
		err := k8sClient.Create(ctx, tenant)
		Expect(err).To(HaveOccurred())
	})
	It("rejects dbName that isn't a valid PG identifies", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "invalid-db-name",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "1-not-valid",
				StorageSize:  &res,
			},
		}
		err := k8sClient.Create(ctx, tenant)
		Expect(err).To(HaveOccurred())
	})
	It("rejects a PostgresTenant with no storageSize", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-storage-size",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "dbname",
			},
		}
		err := k8sClient.Create(ctx, tenant)
		Expect(err).To(HaveOccurred())
	})
	It("accepts a valid PostgresTenant and defaults postgresVersion to 16", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "acme-prod",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "acme",
				StorageSize:  &res,
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		created := &postgresv1alpha1.PostgresTenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      "acme-prod",
			Namespace: "default",
		}, created)).To(Succeed())
		Expect(created.Spec.PostgresVersion).To(Equal("16"))
	})
})
