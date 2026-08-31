package controller

import (
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	. "github.com/onsi/ginkgo/v2"
	"github.com/rijojohn85/fleetdb/pkg/constants"

	. "github.com/onsi/gomega"
)

var _ = Describe("PostgresTenant backup Controller", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond
	It("creates a CronJob when the tenant requests backups", func() {
		tenant := createTenantWithBackup("drills", "default", "1Gi", "0 3 * * *")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		cronJob := &batchv1.CronJob{}
		Eventually(func() error {
			return k8sClient.Get(ctx, namespaceName("drills-postgres-backup", "default"), cronJob)
		}, timeout, interval).Should(Succeed())

		Expect(cronJob.Spec.Schedule).To(Equal("0 3 * * *"))
		Expect(cronJob.Spec.ConcurrencyPolicy).To(Equal(batchv1.ForbidConcurrent))
		container := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
		Expect(container.Image).To(Equal("postgres:16"))
		Expect(container.Command).To(Equal([]string{"pg_dump"}))
		Expect(cronJob.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))

		password := findEnv(container.Env, constants.PGPasswordEnv)
		Expect(password).NotTo(BeNil())
		Expect(password.ValueFrom.SecretKeyRef.LocalObjectReference.Name).To(Equal("drills-postgres"))
		Expect(password.ValueFrom.SecretKeyRef.Key).To(Equal(constants.PostgresPasswordKey))
	})

	It("updates the CronJob when backup schedule changes", func() {
		tenant := createTenantWithBackup("washers", "default", "1Gi", "0 3 * * *")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		cronJob := &batchv1.CronJob{}
		Eventually(func() error {
			return k8sClient.Get(ctx, namespaceName("washers-postgres-backup", "default"), cronJob)
		}, timeout, interval).Should(Succeed())

		Eventually(func() error {
			if err := k8sClient.Get(ctx, namespaceName("washers", "default"), &tenant); err != nil {
				return err
			}
			tenant.Spec.BackupSchedule = "30 4 * * *"
			return k8sClient.Update(ctx, &tenant)
		}, timeout, interval).Should(Succeed())

		Eventually(func() string {
			if err := k8sClient.Get(ctx, namespaceName("washers-postgres-backup", "default"), cronJob); err != nil {
				return ""
			}
			return cronJob.Spec.Schedule
		}, timeout, interval).Should(Equal("30 4 * * *"))
	})

	It("removes the cronJob when backups are no longer requested", func() {
		tenant := createTenantWithBackup("pincers", "default", "1Gi", "0 3 * * *")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		cronJob := &batchv1.CronJob{}
		Eventually(func() error {
			return k8sClient.Get(ctx, namespaceName("pincers-postgres-backup", "default"), cronJob)
		}, timeout, interval).Should(Succeed())

		Eventually(func() error {
			if err := k8sClient.Get(ctx, namespaceName("pincers", "default"), &tenant); err != nil {
				return err
			}
			tenant.Spec.BackupSchedule = ""
			return k8sClient.Update(ctx, &tenant)
		}, timeout, interval).Should(Succeed())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, namespaceName("pincers-postgres-backup", "default"), &batchv1.CronJob{})
			return apierrors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())
	})
})
