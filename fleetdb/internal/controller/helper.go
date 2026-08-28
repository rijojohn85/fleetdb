package controller

import (
	"github.com/rijojohn85/fleetdb/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
