# Для кого

DevOps/SRE/Platform, администрирующие Kubernetes и желающие масштабировать один Deployment/StatefulSet по CPU-метрикам из Golectra.

## Что вы получаете

Контроллер с CRD CPUBasedAutoscaler (namespaced), который:

* читает CPU% из Golectra `/api/v1/snapshot`,

* сглаживает SES (alpha, warmupPoints),

* считает реплики: `ceil(currentReplicas * S_t / targetCPUPercent)`,

* применяет min/max, ограничения шага, окно стабилизации и режим freeze,

* патчит `spec.replicas` целевого ворклода,

* даёт api: `/healthz`, `/readyz`, `/metrics`, `POST /api/v1/preview-plan`.

## Поддерживаемый env

* Kubernetes: 1.24–1.30+.

* Контейнерный рантайм: любой поддерживаемый K8s (containerd/CRI-O).

* Prometheus (опционально) для сбора метрик контроллера.

* Доступность Golectra по HTTP из Pod контроллера.

## Доступы и RBAC

Минимальные:

* Чтение/наблюдение CPUBasedAutoscaler, обновление status.

* Get/patch apps/{deployments,statefulsets}.

* Read HPA/KEDA (конфликты).

* Создание/patch Events; Leases для leader election.
Режимы: кластерный (по умолчанию) либо ограниченный namespace (--namespace-scope).

## Ресурсы

* Контроллер: CPU ≤ 100m, RAM ≤ 128Mi (настраивается в values).

* Цикл: один цикл ≤ 0,5 * dtSeconds (при превышении — Event).

## Ограничения

* Один скейлер на workload: HPA/KEDA не должны таргетить тот же Deployment/StatefulSet.

* Контроллер детерминированный и идемпотентный; меняет только spec.replicas.

* CPU% в Golectra трактуется как процент загрузки пода в диапазоне [0..100].