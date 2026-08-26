# Summary

[Introduction](./introduction.md)

# Phase 0 — Environment

- [Toolchain Setup and OLM Smoke Test](./ch00_toolchain_and_olm.md)

# Phase 1 — Core API and Controller

- [The PostgresTenant API and Project Scaffold](./ch01_api_and_scaffold.md)
- [The Reconciler](./ch02_reconciler.md)
- [Status Conditions and Requeue Strategy](./ch03_status_and_requeue.md)
- [Scheduled Backups: a Second Controller](./ch04_scheduled_backups.md)

# Phase 2 — Observability, From Zero

- [What Are Metrics, Logs, and Traces?](./ch05_observability_concepts.md)
- [Metrics in Practice](./ch06_metrics.md)
- [Logs in Practice](./ch07_logs.md)
- [Traces in Practice](./ch08_traces.md)
- [Prometheus, Grafana, and Auto-Provisioned Dashboards](./ch09_prometheus_grafana_dashboards.md)
- [pgAdmin Per Tenant](./ch10_pgadmin.md)

# Phase 3 — controller-runtime Internals, Hands-On

- [Watching Pods with a Raw Informer](./ch11_raw_informers.md)
- [Tracing a Reconcile Call](./ch12_reconcile_trace.md)
- [Leader Election and Failover](./ch13_leader_election.md)

# Phase 4 — Operator SDK's Extras

- [Conversion Webhooks: v1alpha1 to v1](./ch14_conversion_webhooks.md)
- [Admission Webhooks](./ch15_admission_webhooks.md)
- [Bundle Generation and the CSV](./ch16_bundle_and_csv.md)
- [Custom Scorecard Tests](./ch17_scorecard.md)
- [OLM Install on Kind](./ch18_olm_install.md)
- [Upgrade Path: v1alpha1 to v1](./ch19_upgrade_path.md)
