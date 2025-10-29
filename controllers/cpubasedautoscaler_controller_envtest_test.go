package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/vshulcz/cpu-autoscaler/api/v1alpha1"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func TestReconcile_ScaleUpAndStatusUpdate(t *testing.T) {
	ns := "default"
	deployName := "my-app"
	crName := "my-app-cpuba"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"gauges": map[string]float64{
				"CPUutilization1": 80.0,
				"CPUutilization2": 70.0,
			},
			"counters": map[string]int64{"PollCount": 123},
		})
	}))
	defer srv.Close()

	rep := int32(3)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: deployName, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &rep,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
			Template: corePodTemplate(map[string]string{"app": "x"}),
		},
	}
	if err := k8sClient.Create(ctx, d); err != nil {
		t.Fatalf("create deploy: %v", err)
	}

	cr := &v1.CPUBasedAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: ns},
		Spec: v1.CPUBasedAutoscalerSpec{
			TargetRef: v1.TargetReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deployName,
				Namespace:  ns,
			},
			Source: v1.SourceSpec{
				Type: v1.SourceGolectra,
				Golectra: &v1.GolectraSource{
					BaseURL:      srv.URL,
					SnapshotPath: "/api/v1/snapshot",
					TimeoutMs:    800,
					CPUKeyPrefix: "CPUutilization",
				},
			},
			Control: v1.ControlSpec{
				Mode:             v1.ControlModeReactive,
				TargetCPUPercent: 60,
				Alpha:            0.5,
				WarmupPoints:     1,
				DTSeconds:        15,
			},
			Policy: v1.PolicySpec{
				MinReplicas:            1,
				MaxReplicas:            50,
				MaxStepUp:              5,
				MaxStepDown:            3,
				StabilizationWindowSec: 60,
				ErrorFreezeSeconds:     60,
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	rec := record.NewFakeRecorder(128)
	fc := &fakeClock{t: time.Now()}
	r := NewCPUBAReconciler(k8sClient, scheme, rec, fc)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: k8stypes.NamespacedName{
		Namespace: ns, Name: crName,
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got appsv1.Deployment
	if err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: ns, Name: deployName}, &got); err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 4 {
		t.Fatalf("replicas want 4, got %v", derefI32(got.Spec.Replicas))
	}

	var out v1.CPUBasedAutoscaler
	if err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: ns, Name: crName}, &out); err != nil {
		t.Fatalf("get CR: %v", err)
	}
	if out.Status.Reason == "" || out.Status.AppliedReplicas == 0 {
		t.Fatalf("status not updated: %+v", out.Status)
	}
	if out.Status.AppliedReplicas != 4 {
		t.Fatalf("applied want 4, got %d", out.Status.AppliedReplicas)
	}
}

func TestReconcile_FreezeOnMetricsError(t *testing.T) {
	ns := "default"
	deployName := "bad-app"
	crName := "bad-app-cpuba"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rep := int32(6)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: deployName, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &rep,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "y"}},
			Template: corePodTemplate(map[string]string{"app": "y"}),
		},
	}
	if err := k8sClient.Create(ctx, d); err != nil {
		t.Fatalf("create deploy: %v", err)
	}

	cr := &v1.CPUBasedAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: ns},
		Spec: v1.CPUBasedAutoscalerSpec{
			TargetRef: v1.TargetReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: deployName, Namespace: ns,
			},
			Source: v1.SourceSpec{
				Type: v1.SourceGolectra,
				Golectra: &v1.GolectraSource{
					BaseURL:      srv.URL,
					SnapshotPath: "/api/v1/snapshot",
					TimeoutMs:    200,
					CPUKeyPrefix: "CPUutilization",
				},
			},
			Control: v1.ControlSpec{
				Mode:             v1.ControlModeReactive,
				TargetCPUPercent: 60,
				Alpha:            0.4,
				WarmupPoints:     3,
				DTSeconds:        15,
			},
			Policy: v1.PolicySpec{
				MinReplicas:            1,
				MaxReplicas:            50,
				MaxStepUp:              5,
				MaxStepDown:            3,
				StabilizationWindowSec: 60,
				ErrorFreezeSeconds:     60,
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	rec := record.NewFakeRecorder(128)
	fc := &fakeClock{t: time.Now()}
	r := NewCPUBAReconciler(k8sClient, scheme, rec, fc)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: k8stypes.NamespacedName{
		Namespace: ns, Name: crName,
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got appsv1.Deployment
	if err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: ns, Name: deployName}, &got); err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 6 {
		t.Fatalf("replicas should stay 6 on freeze, got %v", derefI32(got.Spec.Replicas))
	}

	var out v1.CPUBasedAutoscaler
	if err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: ns, Name: crName}, &out); err != nil {
		t.Fatalf("get CR: %v", err)
	}
	if out.Status.Reason != v1.ReasonFreeze {
		t.Fatalf("reason should be freeze, got %s", out.Status.Reason)
	}
}

func corePodTemplate(labels map[string]string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corePodSpec(),
	}
}

func corePodSpec() corev1.PodSpec {
	return corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:    "c",
			Image:   "busybox",
			Command: []string{"sh", "-c", "sleep 3600"},
		}},
	}
}

func derefI32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
