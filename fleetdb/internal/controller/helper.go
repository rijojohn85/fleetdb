package controller

import (
	"github.com/rijojohn85/fleetdb/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func mustQuantity(res string) *resource.Quantity {
	q := resource.MustParse(res)
	return &q
}

func createTenant(name, namespace, res string) v1alpha1.PostgresTenant {
	return v1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.PostgresTenantSpec{
			DatabaseName: name,
			StorageSize:  mustQuantity(res),
		},
	}
}

func findEnv(envs []corev1.EnvVar, name string) *corev1.EnvVar {
	for _, env := range envs {
		if env.Name == name {
			return &env
		}
	}
	return nil
}

func createTenantWithBackup(n, ns, res, cron string) v1alpha1.PostgresTenant {
	t := createTenant(n, ns, res)
	t.Spec.BackupSchedule = cron
	return t
}

func namespaceName(n, ns string) types.NamespacedName {
	return types.NamespacedName{Name: n, Namespace: ns}
}
