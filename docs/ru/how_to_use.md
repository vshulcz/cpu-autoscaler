# Как попробовать локально

## Требования

* Docker (Desktop/Engine) запущен
* Установлены: kubectl, helm, kind, jq
* Есть исходники двух репозиториев:
    * GOLECTRA_DIR — корень вашего Golectra (там, где cmd/server/main.go)
    * AUTOSCALER_DIR — корень автоскейлера (там, где чарт deploy/helm/cpu-autoscaler и Dockerfile контроллера)

## Шаги

1. Создаём чистый кластер kind
```bash
kind delete cluster --name cpuba-dev >/dev/null 2>&1 || true
kind create cluster --name cpuba-dev --image kindest/node:v1.34.0
```

2. Собираем golectra и грузим образ в kind
```bash
# В директории golectra
docker build -t golectra-server:dev -f cmd/server/Dockerfile .
kind load docker-image --name cpuba-dev golectra-server:dev
```

3. Собираем autoscaler и грузим образ в kind
```bash
# В директории cpu-autoscaler
docker build -t cpu-autoscaler:dev .
kind load docker-image --name cpuba-dev cpu-autoscaler:dev
```

4. Неймспейсы
```bash
kubectl create ns autoscaler || true
kubectl create ns demo || true
```

5. Устанавливаем контроллер (Helm)
```bash
# В директории cpu-autoscaler
helm upgrade --install cpuba "./deploy/helm/cpu-autoscaler" \
  -n autoscaler \
  --set image.repository=cpu-autoscaler \
  --set image.tag=dev \
  --set image.pullPolicy=IfNotPresent \
  --set controller.leaderElection=true \
  --set controller.logLevel=info \
  --set controller.metricsBindAddress=":9090" \
  --set controller.metricsSecure=false \
  --set controller.httpBind=":8080" \
  --set controller.healthProbeBindAddress=":8081"

kubectl -n autoscaler rollout status deploy/cpu-autoscaler-cpuba
```

6. Деплой Golectra и демо-приложения
```bash
# В директории cpu-autoscaler
kubectl apply -f deploy/k8s/golectra.yaml
kubectl -n demo rollout status deploy/golectra
kubectl apply -f deploy/k8s/demo-app.yaml
kubectl -n demo rollout status deploy/demo-app
```

7. Создаём CR CPUBasedAutoscaler 
```bash
# В директории cpu-autoscaler
kubectl apply -f deploy/k8s/cpuba.yaml
kubectl -n demo get cpubasedautoscalers.autoscale.example.com
```

8. Прогон: подаём метрики и смотрим масштабирование

В одном терминале:
```bash
kubectl -n demo port-forward svc/golectra 18080:8080 >/dev/null 2>&1 &
sleep 1

kubectl -n demo get deploy demo-app -w
```

Во втором терминале:
```bash
# 90% на двух "ядрах"
curl -s -X POST http://localhost:18080/update/gauge/CPUutilization1/90
curl -s -X POST http://localhost:18080/update/gauge/CPUutilization2/90
```

* Можно видеть в первом терминале, как количество реплик растет

Во втором терминале:
```bash
# 10% на двух "ядрах"
curl -s -X POST http://localhost:18080/update/gauge/CPUutilization1/10
curl -s -X POST http://localhost:18080/update/gauge/CPUutilization2/10
```

* Можно видеть в первом терминале, как количество реплик падает

9. Preview-plan и метрики контроллера
```bash
kubectl -n autoscaler port-forward svc/cpu-autoscaler-cpuba 8080:8080 9090:9090 >/dev/null 2>&1 &
sleep 1

curl -s -X POST http://localhost:8080/api/v1/preview-plan \
  -H 'Content-Type: application/json' \
  -d '{"namespace":"demo","name":"demo-app-cpuba"}' | jq

curl -s http://localhost:9090/metrics | head
```

### Чтобы посмотреть логи контейнера автоскейлера:
```bash
kubectl -n autoscaler logs deploy/cpu-autoscaler-cpuba --tail=200
```