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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresTenantSpec defines the desired state of PostgresTenant.
type PostgresTenantSpec struct {
	// DatabaseName is the name of the database created for this
	// tenant. It must be a valid Postgres identifies: lowecase letter,
	// digits and underscores, but must not begin with a digit or underscore.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	// +kubebuilder:validation:MaxLength=63
	DatabaseName string `json:"databaseName"`

	// StorageSize is how much storage to request for the tenant's
	// PersistentVolumeClaim, eg "10Gi"
	// +kubebuilder:validation:Required
	StorageSize *resource.Quantity `json:"storageSize"`

	// StorageClassName selects which storageClass to provision the PVC
	// from. If empty, the cluster's default StorageClass will be used
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// PostgresVersion is the Postgres image tag to run.
	// +kubebuilder:default="16"
	// +optional
	PostgresVersion string `json:"postgresVersion,omitempty"`
}

// PostgresTenantStatus defines the observed state of PostgresTenant.
type PostgresTenantStatus struct {
	// ObservedGeneration is the most recently
	// reconciled generation of this PostgresTenant's Spec
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PostgresTenant is the Schema for the postgrestenants API.
type PostgresTenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresTenantSpec   `json:"spec,omitempty"`
	Status PostgresTenantStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresTenantList contains a list of PostgresTenant.
type PostgresTenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresTenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresTenant{}, &PostgresTenantList{})
}
