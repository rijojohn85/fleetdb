package controller

import (
	"context"

	"github.com/rijojohn85/fleetdb/api/v1alpha1"
	"github.com/rijojohn85/fleetdb/pkg/constants"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type BackupReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete

func backupCronJobName(tenant *v1alpha1.PostgresTenant) string {
	return resourceName(tenant) + "-backup"
}

func (b *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("Name", req.Name, "Namespace", req.Namespace)
	var tenant v1alpha1.PostgresTenant
	if err := b.Get(ctx, req.NamespacedName, &tenant); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("tenant not found, probably deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if tenant.Spec.BackupSchedule == "" {
		err := b.deleteBackupCronJob(ctx, &tenant)
		if err != nil {
			log.Error(err, "error deleting cronJob")
		}
		return ctrl.Result{}, err
	}
	if err := b.reconcileBackupCronJob(ctx, &tenant); err != nil {
		log.Error(err, "error reconciling backup")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (b *BackupReconciler) deleteBackupCronJob(
	ctx context.Context,
	tenant *v1alpha1.PostgresTenant,
) error {
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupCronJobName(tenant),
			Namespace: tenant.Namespace,
		},
	}
	err := b.Delete(ctx, cronJob)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (b *BackupReconciler) reconcileBackupCronJob(ctx context.Context, tenant *v1alpha1.PostgresTenant) error {
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupCronJobName(tenant),
			Namespace: tenant.Namespace,
		},
	}
	operation, err := controllerutil.CreateOrUpdate(ctx, b.Client, cronJob, func() error {
		applyBackupCronjobSpec(cronJob, tenant)
		return ctrl.SetControllerReference(tenant, cronJob, b.Scheme)
	})
	if operation == controllerutil.OperationResultCreated {
		b.Recorder.Event(tenant, corev1.EventTypeNormal, constants.BackupScheduledReason, "Scheduled database backups at "+tenant.Spec.BackupSchedule)
		fleetDBBackupsScheduled.Inc()
	}
	return err
}

func applyBackupCronjobSpec(cronJob *batchv1.CronJob, tenant *v1alpha1.PostgresTenant) {
	cronJob.Spec.Schedule = tenant.Spec.BackupSchedule
	cronJob.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
	cronJob.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:    "pg-dump",
			Image:   "postgres:" + tenant.Spec.PostgresVersion,
			Command: []string{"pg_dump"},
			Env: []corev1.EnvVar{
				{Name: constants.PGHostEnv, Value: resourceName(tenant)},
				{Name: constants.PGDatabaseEnv, Value: tenant.Spec.DatabaseName},
				{Name: constants.PGUserEnv, Value: constants.PostgresUserValue},
				{
					Name: constants.PGPasswordEnv, ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: resourceName(tenant),
							},
							Key: constants.PostgresPasswordKey,
						},
					},
				},
			},
		},
	}
}

func (b *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresTenant{}).
		Named("backup").
		Owns(&batchv1.CronJob{}).
		Complete(b)
}
