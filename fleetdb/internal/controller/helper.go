package controller

import "k8s.io/apimachinery/pkg/api/resource"

func mustQuantity(res string) *resource.Quantity {
	q := resource.MustParse(res)
	return &q
}
