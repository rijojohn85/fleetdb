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

// Package controller implements the controllers for fleetdb
package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	postgresv1alpha1 "github.com/rijojohn85/fleetdb/api/v1alpha1"
	"github.com/rijojohn85/fleetdb/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresTenantReconciler reconciles a PostgresTenant object
type PostgresTenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// resourceNmae is the name every owned resource for a tenant shares
// eg: "acme-postgres" for a tenant name "acme"
func resourceName(tenant *postgresv1alpha1.PostgresTenant) string {
	return tenant.Name + "-postgres"
}

// generatePassword returns a random, base64 encoded password.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func desiredSecret(tenant *postgresv1alpha1.PostgresTenant) (*corev1.Secret, error) {
	password, err := generatePassword()
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
		StringData: map[string]string{
			constants.POSTGRES_USER_KEY:     constants.POSTGRES_USER_VALUE,
			constants.POSTGRES_PASSWORD_KEY: password,
			constants.POSTGRES_DB_KEY:       tenant.Spec.DatabaseName,
		},
	}, nil
}

// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PostgresTenant object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *PostgresTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("Name", req.Name, "Namespace", req.Namespace)
	log.Info("starting reconciliation")

	var tenant postgresv1alpha1.PostgresTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to fetch tenant")
			return ctrl.Result{}, err
		} else {
			log.Info("tenant not found, probably deleted.")
			return ctrl.Result{}, nil
		}
	}

	var existing corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Name: resourceName(&tenant), Namespace: tenant.Namespace}, &existing)
	if err == nil {
		log.Info("secret already exists, nothing to do.")
		return ctrl.Result{}, nil
	}
	if !apierrors.IsNotFound(err) {
		log.Error(err, "fetching secret")
	}

	secret, err := desiredSecret(&tenant)
	if err != nil {
		log.Error(err, "creating secret object")
	}
	if err := ctrl.SetControllerReference(&tenant, secret, r.Scheme); err != nil {
		log.Error(err, "setting controller reference to secret")
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, secret); err != nil {
		log.Error(err, "error creating secret with client")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PostgresTenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1alpha1.PostgresTenant{}).
		Named("postgrestenant").
		Complete(r)
}
