package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var _ = Describe("FleetDB metrics", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond
	It("counts generated credential secrets", func() {
		before := testutil.ToFloat64(fleetDBCredentialsGenerated)
		tenant := createTenant("clamps", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())
		Eventually(func() float64 {
			return testutil.ToFloat64(fleetDBCredentialsGenerated)
		}, timeout, interval).Should(BeNumerically(">", before))
	})

	It("counts scheduled backups", func() {
		before := testutil.ToFloat64(fleetDBBackupsScheduled)

		tenant := createTenantWithBackup("vices", "default", "1Gi", "0 3 * * *")

		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		Eventually(func() float64 {
			return testutil.ToFloat64(fleetDBBackupsScheduled)
		}, timeout, interval).Should(BeNumerically(">", before))
	})
})
