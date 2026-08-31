package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rijojohn85/fleetdb/api/v1alpha1"
	"github.com/rijojohn85/fleetdb/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	timeout  = time.Second * 5
	interval = time.Millisecond * 500
)

var _ = Describe("PostgresTenant reconciller tests", func() {
	It("creates a secret holding the tenant's db creds", func() {
		tenant := createTenant("acme", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())
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

	It("creates a pvc  sized from the tenant's spec", func() {
		tenant := createTenant("widgets", "default", "5Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		var pvc corev1.PersistentVolumeClaim
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "widgets-postgres", Namespace: "default"}, &pvc)
		}, timeout, interval).Should(Succeed())
		Expect(pvc.Spec.Resources.Requests.Storage().String()).To(Equal("5Gi"))
		Expect(pvc.Spec.AccessModes).To(ConsistOf(corev1.ReadWriteOnce))
	})

	It("creates a headless Service on PG port", func() {
		tenant := createTenant("gizmos", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())
		var svc corev1.Service
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "gizmos-postgres", Namespace: "default"}, &svc)
		}, timeout, interval).Should(Succeed())
		Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
		Expect(svc.Spec.Ports).To(ConsistOf(corev1.ServicePort{
			Name: "postgres", Port: 5432,
			TargetPort: intstr.FromInt32(5432),
			Protocol:   corev1.ProtocolTCP,
		}))
		Expect(svc.Spec.Selector).To(Equal(map[string]string{"postgrestenant": "gizmos"}))
	})

	It("creates a single-replica StatefulSet running the requested PG version", func() {
		tenant := createTenant("sprockets", "default", "1Gi")
		tenant.Spec.PostgresVersion = "15"
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		var sts appsv1.StatefulSet
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "sprockets-postgres", Namespace: tenant.Namespace}, &sts)
		}, timeout, interval).Should(Succeed())

		Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
		Expect(sts.Spec.ServiceName).To(Equal("sprockets-postgres"))
		Expect(sts.Spec.Selector.MatchLabels).To(Equal(map[string]string{constants.SelectorLabelKey: "sprockets"}))
		Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("postgres:15"))
		Expect(sts.Spec.Template.Spec.Containers[0].EnvFrom[0].SecretRef.Name).To(Equal("sprockets-postgres"))
		Expect(sts.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath).To(Equal("/var/lib/postgresql/data"))
		Expect(sts.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName).To(Equal("sprockets-postgres"))
	})

	It("updates an existing StatefulSet's image when postgresVersion changes", func() {
		tenant := &v1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{Name: "cogs", Namespace: "default"},
			Spec: v1alpha1.PostgresTenantSpec{
				DatabaseName:    "cogs",
				StorageSize:     mustQuantity("1Gi"),
				PostgresVersion: "15",
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		sts := &appsv1.StatefulSet{}
		Eventually(func() string {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "cogs-postgres", Namespace: "default",
			}, sts); err != nil {
				return ""
			}
			return sts.Spec.Template.Spec.Containers[0].Image
		}, timeout, interval).Should(Equal("postgres:15"))

		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "cogs", Namespace: "default"}, tenant); err != nil {
				return err
			}
			tenant.Spec.PostgresVersion = "16"
			return k8sClient.Update(ctx, tenant)
		}, timeout, interval).Should(Succeed())

		Eventually(func() string {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "cogs-postgres", Namespace: "default",
			}, sts); err != nil {
				return ""
			}
			return sts.Spec.Template.Spec.Containers[0].Image
		}, timeout, interval).Should(Equal("postgres:16"))
	})

	It("reports Ready=False while the StatefulSet is starting", func() {
		tenant := createTenant("bolts", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		Eventually(func() string {
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bolts", Namespace: "default"}, &tenant); err != nil {
				return ""
			}
			cond := meta.FindStatusCondition(tenant.Status.Conditions, constants.ConditionReady)
			if cond == nil {
				return ""
			}
			return string(cond.Status)
		}, timeout, interval).Should(Equal("False"))
		Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))
		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bolts", Namespace: "default"}, &tenant); err != nil {
				return err
			}
			tenant.Spec.PostgresVersion = "17"
			return k8sClient.Update(ctx, &tenant)
		}, timeout, interval).Should(Succeed())

		Eventually(func() int64 {
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bolts", Namespace: "default"}, &tenant); err != nil {
				return 0
			}
			return tenant.Status.ObservedGeneration
		})
	})

	It("reports Ready=True once the StatefulSet reports a ready replica", func() {
		tenant := createTenant("hammers", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())
		sts := &appsv1.StatefulSet{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "hammers-postgres", Namespace: "default"}, sts)
		}, timeout, interval).Should(Succeed())
		Eventually(func() error {
			if err := k8sClient.Get(ctx, namespaceName("hammers-postgres", "default"), sts); err != nil {
				return err
			}
			sts.Status.Replicas = 1
			sts.Status.ReadyReplicas = 1
			return k8sClient.Status().Update(ctx, sts)
		}, timeout, interval).Should(Succeed())

		Eventually(func() string {
			if err := k8sClient.Get(ctx, namespaceName("hammers", "default"), &tenant); err != nil {
				return ""
			}
			cond := meta.FindStatusCondition(tenant.Status.Conditions, constants.ConditionReady)
			if cond == nil {
				return ""
			}
			return string(cond.Status)
		}, timeout, interval).Should(Equal("True"))
		events := &corev1.EventList{}
		Eventually(func() bool {
			if err := k8sClient.List(ctx, events, client.InNamespace("default")); err != nil {
				return false
			}
			for _, ev := range events.Items {
				if ev.InvolvedObject.Name == "hammers" && ev.Reason == "TenantReady" {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})
	It("recreates The Service if someone deletes it", func() {
		tenant := createTenant("screws", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())

		svc := &corev1.Service{}
		Eventually(func() error {
			return k8sClient.Get(ctx, namespaceName("screws-postgres", "default"), svc)
		}, timeout, interval).Should(Succeed())
		Expect(k8sClient.Delete(ctx, svc)).To(Succeed())

		Eventually(func() error {
			return k8sClient.Get(ctx, namespaceName("screws-postgres", "default"), &corev1.Service{})
		}, timeout, interval).Should(Succeed())
	})

	It("recors an event when it creates the Secret", func() {
		tenant := createTenant("nuts", "default", "1Gi")
		Expect(k8sClient.Create(ctx, &tenant)).To(Succeed())
		events := &corev1.EventList{}

		Eventually(func() bool {
			if err := k8sClient.List(ctx, events, client.InNamespace("default")); err != nil {
				return false
			}
			for _, ev := range events.Items {
				if ev.InvolvedObject.Name == "nuts" && ev.Reason == constants.SecretCreatedReason {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})
})
