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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	postgresv1alpha1 "github.com/rijojohn85/fleetdb/api/v1alpha1"
	"github.com/rijojohn85/fleetdb/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresTenantReconciler reconciles a PostgresTenant object
type PostgresTenantReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
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
			constants.PostgresUserKey:     constants.PostgresUserValue,
			constants.PostgresPasswordKey: password,
			constants.PostgresDBKey:       tenant.Spec.DatabaseName,
		},
	}, nil
}

func desiredPVC(tenant *postgresv1alpha1.PostgresTenant) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *tenant.Spec.StorageSize,
				},
			},
		},
	}
	if tenant.Spec.StorageClassName != "" {
		pvc.Spec.StorageClassName = &tenant.Spec.StorageClassName
	}
	return pvc
}

func selectorLabels(tenant *postgresv1alpha1.PostgresTenant) map[string]string {
	return map[string]string{constants.SelectorLabelKey: tenant.Name}
}

func desiredService(tenant *postgresv1alpha1.PostgresTenant) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  selectorLabels(tenant),
			Ports: []corev1.ServicePort{
				{
					Name:       "postgres",
					Port:       5432,
					TargetPort: intstr.FromInt32(5432), Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
	return svc
}

func applyStatefulSetSpec(sts *appsv1.StatefulSet, tenant *postgresv1alpha1.PostgresTenant) {
	imageName := "postgres:"
	if tenant.Spec.PostgresVersion != "" {
		imageName += tenant.Spec.PostgresVersion
	} else {
		imageName += "16"
	}
	replicas := int32(1)
	sts.Spec = appsv1.StatefulSetSpec{
		Replicas:    &replicas,
		ServiceName: resourceName(tenant),
		Selector: &metav1.LabelSelector{
			MatchLabels: selectorLabels(tenant),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: selectorLabels(tenant),
			},
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: resourceName(tenant),
							},
						},
					},
				},

				Containers: []corev1.Container{
					{
						Name:  resourceName(tenant),
						Image: imageName,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "data",
								MountPath: "/var/lib/postgresql/data",
							},
						},
						EnvFrom: []corev1.EnvFromSource{
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: resourceName(tenant),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *PostgresTenantReconciler) reconcileStatefulSet(ctx context.Context, tenant *postgresv1alpha1.PostgresTenant) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
	}

	operation, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		applyStatefulSetSpec(sts, tenant)
		return ctrl.SetControllerReference(tenant, sts, r.Scheme)
	})
	switch operation {
	case controllerutil.OperationResultCreated:
		r.Recorder.Event(tenant, corev1.EventTypeNormal, constants.StatefulSetCreatedReason, "Created the PG stateful set")
	case controllerutil.OperationResultUpdated:
		r.Recorder.Event(tenant, corev1.EventTypeNormal, constants.StatefulSetUpdatedReason, "Updated the PG stateful set")
	}
	return sts, err
}

// stsReady reports whether the StatefulSet has every replica it wants
// actually running and ready
func stsReady(sts *appsv1.StatefulSet) bool {
	return sts.Status.ReadyReplicas == *sts.Spec.Replicas
}

func (r *PostgresTenantReconciler) updateStatus(ctx context.Context, tenant *postgresv1alpha1.PostgresTenant, sts *appsv1.StatefulSet) error {
	condition := metav1.Condition{
		Type:               constants.ConditionReady,
		ObservedGeneration: tenant.Generation,
		Status:             metav1.ConditionFalse,
		Reason:             "Provisioning",
		Message:            "Waiting for the postgrs replica to become ready",
	}
	if stsReady(sts) {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Ready"
		condition.Message = "Postgres replica is ready"
	}
	prev := meta.FindStatusCondition(tenant.Status.Conditions, constants.ConditionReady)
	wasReady := prev != nil && prev.Status == metav1.ConditionTrue
	changed := meta.SetStatusCondition(&tenant.Status.Conditions, condition)
	if tenant.Status.ObservedGeneration != tenant.Generation {
		tenant.Status.ObservedGeneration = tenant.Generation
		changed = true
	}
	if !changed {
		return nil
	}
	switch {
	case condition.Status == metav1.ConditionTrue && !wasReady:
		r.Recorder.Event(tenant, corev1.EventTypeNormal, constants.TenantReadyReason, condition.Message)
	case condition.Status == metav1.ConditionFalse && wasReady:
		r.Recorder.Event(tenant, corev1.EventTypeWarning, constants.TenantNotReadyReason, condition.Message)
	}
	return r.Status().Update(ctx, tenant)
}

// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete

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
	resName := resourceName(&tenant)
	var existingSecret corev1.Secret
	if created, err := r.reconcileCreateOnce(ctx, &tenant, &existingSecret, func() (client.Object, error) {
		return desiredSecret(&tenant)
	}); err != nil {
		log.Error(err, "fetching or creating secret", "secret", resName)
		return ctrl.Result{}, err
	} else {
		if created {
			log.Info("secret created", "secret", resName)
			r.Recorder.Event(&tenant, corev1.EventTypeNormal, constants.SecretCreatedReason, "Generated Database credentials")
			fleetDBCredentialsGenerated.Inc()
		}
	}

	var existingPVC corev1.PersistentVolumeClaim
	if created, err := r.reconcileCreateOnce(ctx, &tenant, &existingPVC, func() (client.Object, error) {
		return desiredPVC(&tenant), nil
	}); err != nil {
		log.Error(err, "fetching or creating pvc", "pvc", resName)
		return ctrl.Result{}, err
	} else {
		if created {
			log.Info("pvc created", "pvc", resName)
			r.Recorder.Event(&tenant, corev1.EventTypeNormal, constants.PVCCreatedReason, "PVC created")
		}
	}

	var existingService corev1.Service
	if created, err := r.reconcileCreateOnce(ctx, &tenant, &existingService, func() (client.Object, error) {
		return desiredService(&tenant), nil
	}); err != nil {
		log.Error(err, "fetching or creating service", "service", resName)
		return ctrl.Result{}, err
	} else {
		if created {
			log.Info("service create", "service", resName)
			r.Recorder.Event(&tenant, corev1.EventTypeNormal, constants.ServiceCreatedReason, "Service created")
		}
	}

	sts, err := r.reconcileStatefulSet(ctx, &tenant)
	if err != nil {
		log.Error(err, "error reconciling statefulset")
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &tenant, sts); err != nil {
		log.Error(err, "failed to updateStatus")
		return ctrl.Result{}, err
	}
	if !stsReady(sts) {
		log.Info("statefulset not ready, requeuing...", "statefulset", sts)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	log.Info("succesfully reconciled tenant")
	return ctrl.Result{}, nil
}

func (r *PostgresTenantReconciler) reconcileCreateOnce(
	ctx context.Context,
	tenant *postgresv1alpha1.PostgresTenant,
	existing client.Object,
	build func() (client.Object, error),
) (bool, error) {
	key := types.NamespacedName{Name: resourceName(tenant), Namespace: tenant.Namespace}
	err := r.Get(ctx, key, existing)
	if err == nil {
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}

	obj, err := build()
	if err != nil {
		return false, err
	}
	if err := ctrl.SetControllerReference(tenant, obj, r.Scheme); err != nil {
		return false, err
	}
	return true, r.Create(ctx, obj)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PostgresTenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1alpha1.PostgresTenant{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).Owns(&appsv1.StatefulSet{}).
		Named("postgrestenant").
		Complete(r)
}
