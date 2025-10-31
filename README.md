# cpu-autoscaler

Small, purpose-built Kubernetes controller that scales a single Deployment/StatefulSet by patching `spec.replicas` to maintain a target CPU% per pod, using Golectra as the metric source.
It smooths incoming CPU% with SES and applies step limits, stabilization window, and freeze on errors.

## Documentation

### 🇬🇧 English
- [Prerequisites & Scope](docs/en/prerequisites_scope.md)

### 🇷🇺 Russian
- [Предпосылки](docs/ru/prerequisites_scope.md)  
- [Как попробовать (поднять локально, проверить скейлинг)](docs/ru/how_to_use.md)

## Issues & Support

If something doesn’t work as expected:

* Check controller logs: kubectl -n autoscaler logs deploy/cpu-autoscaler-<release> --tail=200
* Read Events/Status on your CPUBasedAutoscaler for reasons like forecast|max_step|stabilized|freeze

Feel free to open an issue with:

* your K8s version,
* controller logs,
* kubectl describe of the CR,
* and the Golectra /api/v1/snapshot sample.