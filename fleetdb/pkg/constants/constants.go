// Package constants holds constants for the controllers
package constants

const (
	PostgresUserValue        = "postgres"
	PostgresUserKey          = "POSTGRES_USER"
	PostgresPasswordKey      = "POSTGRES_PASSWORD"
	PostgresDBKey            = "POSTGRES_DB"
	SelectorLabelKey         = "postgrestenant"
	ConditionReady           = "Ready"
	SecretCreatedReason      = "SecretCreated"
	StatefulSetCreatedReason = "StatefulSetCreated"
	StatefulSetUpdatedReason = "StatefulSetUpdated"
	ServiceCreatedReason     = "ServiceCreated"
	PVCCreatedReason         = "PVCCreated"
	TenantReadyReason        = "TenantReady"
	TenantNotReadyReason     = "TenantNotReady"
	BackupScheduledReason    = "BackupScheduled"
	PGHostEnv                = "PGHOST"
	PGDatabaseEnv            = "PGDATABASE"
	PGUserEnv                = "PGUSER"
	PGPasswordEnv            = "PGPASSWORD"
)
