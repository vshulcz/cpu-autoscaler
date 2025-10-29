package controllers

import (
	"context"
	"fmt"
	"math"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/vshulcz/cpu-autoscaler/api/v1alpha1"
	"github.com/vshulcz/cpu-autoscaler/internal/metrics"
	gcli "github.com/vshulcz/cpu-autoscaler/internal/metrics/golectra"
	"github.com/vshulcz/cpu-autoscaler/internal/plan/policy"
	"github.com/vshulcz/cpu-autoscaler/internal/plan/ses"
)

// +kubebuilder:rbac:groups=autoscale.example.com,resources=cpubasedautoscalers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=autoscale.example.com,resources=cpubasedautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type CPUBasedAutoscalerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Clock    Clock

	sesByKey     map[string]*ses.SES
	plannerByKey map[string]*policy.Planner
}

func NewCPUBAReconciler(c client.Client, sch *runtime.Scheme, rec record.EventRecorder, clk Clock) *CPUBasedAutoscalerReconciler {
	if clk == nil {
		clk = realClock{}
	}
	return &CPUBasedAutoscalerReconciler{
		Client:       c,
		Scheme:       sch,
		Recorder:     rec,
		Clock:        clk,
		sesByKey:     map[string]*ses.SES{},
		plannerByKey: map[string]*policy.Planner{},
	}
}

func (r *CPUBasedAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.CPUBasedAutoscaler{}, builder.WithPredicates()).
		WithOptions(controller.Options{MaxConcurrentReconciles: int(workers)}).
		Complete(r)
}

func (r *CPUBasedAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	cycleStart := r.Clock.Now()

	var a v1.CPUBasedAutoscaler
	if err := r.Get(ctx, req.NamespacedName, &a); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	key := fmt.Sprintf("%s/%s", a.Namespace, a.Name)
	now := r.Clock.Now()

	targetNS := a.Spec.TargetRef.Namespace
	if targetNS == "" {
		targetNS = a.Namespace
	}
	dt := time.Duration(maxI32(a.Spec.Control.DTSeconds, 1)) * time.Second

	conflict, conflictMsg := r.detectConflict(ctx, targetNS, a.Spec.TargetRef.Kind, a.Spec.TargetRef.Name)
	if conflict {
		r.setCondition(&a, "ConflictingController", metav1.ConditionTrue, "Detected", conflictMsg)
		r.patchStatus(ctx, &a, func(st *v1.CPUBasedAutoscalerStatus) {
			st.LastRun = &metav1.Time{Time: now}
			st.LastError = conflictMsg
			st.Reason = v1.ReasonFreeze
		})
		return ctrl.Result{RequeueAfter: dt}, nil
	}
	r.setCondition(&a, "ConflictingController", metav1.ConditionFalse, "None", "")

	current, workloadKind, err := r.getCurrentReplicas(ctx, targetNS, a.Spec.TargetRef)
	if err != nil {
		msg := "failed to get target replicas: " + err.Error()
		r.Recorder.Event(&a, "Warning", "TargetReadError", msg)
		r.setCondition(&a, "Degraded", metav1.ConditionTrue, "TargetReadError", msg)
		r.patchStatus(ctx, &a, func(st *v1.CPUBasedAutoscalerStatus) {
			st.LastRun = &metav1.Time{Time: now}
			st.LastError = msg
			st.CurrentReplicas = current
			st.AppliedReplicas = current
			st.Reason = v1.ReasonFreeze
		})
		return ctrl.Result{RequeueAfter: dt}, nil
	}
	r.setCondition(&a, "Degraded", metav1.ConditionFalse, "OK", "")

	validMetrics := true
	var cpuNow float64
	var lastErr string

	if a.Spec.Source.Type != v1.SourceGolectra || a.Spec.Source.Golectra == nil {
		validMetrics = false
		lastErr = "source.type!=golectra not implemented"
	} else {
		goSrc := a.Spec.Source.Golectra
		timeout := time.Duration(maxI32(goSrc.TimeoutMs, 1)) * time.Millisecond
		cli, err := gcli.New(gcli.Options{
			BaseURL:      goSrc.BaseURL,
			SnapshotPath: orDefault(goSrc.SnapshotPath, "/api/v1/snapshot"),
			Timeout:      timeout,
		})
		if err != nil {
			validMetrics = false
			lastErr = "golectra init error: " + err.Error()
		} else {
			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			snap, err := cli.Snapshot(reqCtx)
			cancel()
			if err != nil {
				validMetrics = false
				lastErr = "golectra fetch error: " + err.Error()
			} else {
				avg, err := metrics.AggregateCPUPercent(snap.Gauges, orDefault(goSrc.CPUKeyPrefix, "CPUutilization"))
				if err != nil {
					validMetrics = false
					lastErr = "cpu aggregate error: " + err.Error()
				} else {
					cpuNow = avg
				}
			}
		}
	}

	se := r.ensureSES(key, float64(a.Spec.Control.Alpha), int(a.Spec.Control.WarmupPoints))
	if !se.Warmed() && a.Status.CPUSmoothPercent > 0 {
		se.Prime(a.Status.CPUSmoothPercent)
	}
	var cpuSmooth float64
	if validMetrics {
		cpuSmooth = se.Observe(cpuNow)
	} else {
		cpuSmooth, _ = se.Value()
	}

	warmed := se.Warmed()

	targetCPU := float64(a.Spec.Control.TargetCPUPercent)
	if targetCPU <= 0 {
		targetCPU = 60
	}

	pl := r.ensurePlanner(key, a)
	dec := pl.Plan(key, policy.Inputs{
		Now:                now,
		CurrentReplicas:    current,
		ForecastCPUPercent: cpuSmooth,
		TargetCPUPercent:   targetCPU,
		ValidMetrics:       validMetrics,
		Warmed:             warmed,
	})

	if ctrl.Log.GetV() == 1 { // debug
		logger.Info("plan",
			"cpuNow%", round1(cpuNow),
			"cpuSmooth%", round1(cpuSmooth),
			"targetCPU%", a.Spec.Control.TargetCPUPercent,
			"current", current,
			"desiredRaw", dec.DesiredRaw,
			"applied", dec.Applied,
			"reason", dec.Reason,
		)
	}

	applied := current
	reason := string(dec.Reason)
	if dec.Reason != policy.ReasonFreeze && dec.Applied != current {
		if err := r.applyReplicasPatch(ctx, targetNS, a.Spec.TargetRef, dec.Applied); err != nil {
			reason = string(v1.ReasonFreeze)
			lastErr = "patch error: " + err.Error()
			r.Recorder.Event(&a, "Warning", "PatchError", lastErr)
		} else {
			applied = dec.Applied
			r.Recorder.Eventf(&a, "Normal", "Scaled",
				"%s/%s %s replicas: %d -> %d (reason=%s)",
				targetNS, a.Spec.TargetRef.Name, workloadKind, current, applied, dec.Reason)
		}
	}

	if lastErr != "" {
		r.setCondition(&a, "Degraded", metav1.ConditionTrue, "Error", lastErr)
	} else {
		r.setCondition(&a, "Degraded", metav1.ConditionFalse, "OK", "")
	}
	if dec.Reason == policy.ReasonFreeze {
		r.setCondition(&a, "Frozen", metav1.ConditionTrue, "FreezeWindow", "freeze active")
	} else {
		r.setCondition(&a, "Frozen", metav1.ConditionFalse, "OK", "")
	}
	r.setCondition(&a, "Ready", metav1.ConditionTrue, "Active", "controller running")

	crReason := v1.DecisionReason(reason)
	if reason == policy.ReasonMaxStep {
		crReason = v1.ReasonMaxStep
	} else if reason == policy.ReasonFreeze {
		crReason = v1.ReasonFreeze
	} else {
		crReason = v1.ReasonForecast
	}

	err = r.patchStatus(ctx, &a, func(st *v1.CPUBasedAutoscalerStatus) {
		st.LastRun = &metav1.Time{Time: now}
		st.LastError = lastErr
		st.CPUNowPercent = round1(cpuNow)
		st.CPUSmoothPercent = round1(cpuSmooth)
		st.CurrentReplicas = current
		st.DesiredReplicas = dec.DesiredRaw
		st.AppliedReplicas = applied
		st.Reason = crReason
	})
	if err != nil {
		logger.Error(err, "status patch failed")
	}

	elapsed := r.Clock.Now().Sub(cycleStart)
	if elapsed > dt/2 {
		r.Recorder.Eventf(&a, "Warning", "CycleBudgetExceeded",
			"cycle took %v (> 0.5*dt=%v); consider tuning dtSeconds or limits", elapsed, dt/2)
		logger.Info("cycle budget exceeded",
			"elapsed", elapsed, "dtSeconds", a.Spec.Control.DTSeconds)
	}

	return ctrl.Result{RequeueAfter: dt}, nil
}

func (r *CPUBasedAutoscalerReconciler) detectConflict(ctx context.Context, ns, kind, name string) (bool, string) {
	var hpas autoscalingv2.HorizontalPodAutoscalerList
	if err := r.List(ctx, &hpas, client.InNamespace(ns)); err == nil {
		for _, h := range hpas.Items {
			if h.Spec.ScaleTargetRef.Kind == kind &&
				h.Spec.ScaleTargetRef.Name == name {
				return true, fmt.Sprintf("HPA %s/%s targets the same %s/%s", ns, h.Name, kind, name)
			}
		}
	}
	var sol unstructured.UnstructuredList
	sol.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    "ScaledObjectList",
	})
	if err := r.List(ctx, &sol, client.InNamespace(ns)); err == nil {
		for _, it := range sol.Items {
			soKind, _, _ := unstructured.NestedString(it.Object, "spec", "scaleTargetRef", "kind")
			soName, _, _ := unstructured.NestedString(it.Object, "spec", "scaleTargetRef", "name")

			matchesKind := (soKind == "" && kind == "Deployment") || (soKind == kind)
			if matchesKind && soName == name {
				return true, fmt.Sprintf("KEDA ScaledObject %s/%s targets the same %s/%s", ns, it.GetName(), kind, name)
			}
		}
	}

	return false, ""
}

func (r *CPUBasedAutoscalerReconciler) getCurrentReplicas(ctx context.Context, ns string, tr v1.TargetReference) (int32, string, error) {
	switch tr.Kind {
	case "Deployment":
		var d appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: tr.Name}, &d); err != nil {
			return 0, "Deployment", err
		}
		if d.Spec.Replicas == nil {
			return 1, "Deployment", nil
		}
		return *d.Spec.Replicas, "Deployment", nil
	case "StatefulSet":
		var s appsv1.StatefulSet
		if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: tr.Name}, &s); err != nil {
			return 0, "StatefulSet", err
		}
		if s.Spec.Replicas == nil {
			return 1, "StatefulSet", nil
		}
		return *s.Spec.Replicas, "StatefulSet", nil
	default:
		return 0, tr.Kind, fmt.Errorf("unsupported targetRef.kind=%s", tr.Kind)
	}
}

func (r *CPUBasedAutoscalerReconciler) applyReplicasPatch(ctx context.Context, ns string, tr v1.TargetReference, replicas int32) error {
	switch tr.Kind {
	case "Deployment":
		var obj appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: tr.Name}, &obj); err != nil {
			return err
		}
		orig := obj.DeepCopy()
		obj.Spec.Replicas = ptrI32(replicas)
		return r.Patch(ctx, &obj, client.MergeFrom(orig))
	case "StatefulSet":
		var obj appsv1.StatefulSet
		if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: tr.Name}, &obj); err != nil {
			return err
		}
		orig := obj.DeepCopy()
		obj.Spec.Replicas = ptrI32(replicas)
		return r.Patch(ctx, &obj, client.MergeFrom(orig))
	default:
		return fmt.Errorf("unsupported targetRef.kind=%s", tr.Kind)
	}
}

func (r *CPUBasedAutoscalerReconciler) patchStatus(ctx context.Context, a *v1.CPUBasedAutoscaler, mut func(st *v1.CPUBasedAutoscalerStatus)) error {
	orig := a.DeepCopy()
	mut(&a.Status)
	return r.Status().Patch(ctx, a, client.MergeFrom(orig))
}

func (r *CPUBasedAutoscalerReconciler) setCondition(a *v1.CPUBasedAutoscaler, condType string, status metav1.ConditionStatus, reason, msg string) {
	conds := a.Status.Conditions
	now := metav1.Now()
	newC := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: a.Generation,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
	}
	conds = setOrUpdateCondition(conds, newC)
	a.Status.Conditions = conds
}

func setOrUpdateCondition(conds []metav1.Condition, c metav1.Condition) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(conds)+1)
	replaced := false
	for _, ex := range conds {
		if ex.Type == c.Type {
			if ex.Status == c.Status {
				c.LastTransitionTime = ex.LastTransitionTime
			}
			out = append(out, c)
			replaced = true
		} else {
			out = append(out, ex)
		}
	}
	if !replaced {
		out = append(out, c)
	}
	return out
}

func (r *CPUBasedAutoscalerReconciler) ensureSES(key string, alpha float64, warmup int) *ses.SES {
	if warmup < 1 {
		warmup = 1
	}
	if alpha <= 0 || alpha > 1 {
		alpha = 0.4
	}
	if m, ok := r.sesByKey[key]; ok {
		if m.Alpha != alpha || m.WarmupN != warmup {
			nm, _ := ses.New(alpha, warmup)
			r.sesByKey[key] = nm
			return nm
		}
		return m
	}
	nm, _ := ses.New(alpha, warmup)
	r.sesByKey[key] = nm
	return nm
}

func (r *CPUBasedAutoscalerReconciler) ensurePlanner(key string, a v1.CPUBasedAutoscaler) *policy.Planner {
	lim := policy.Limits{
		MinReplicas:         a.Spec.Policy.MinReplicas,
		MaxReplicas:         a.Spec.Policy.MaxReplicas,
		MaxStepUp:           a.Spec.Policy.MaxStepUp,
		MaxStepDown:         a.Spec.Policy.MaxStepDown,
		StabilizationWindow: time.Duration(maxI32(a.Spec.Policy.StabilizationWindowSec, 0)) * time.Second,
		ErrorFreeze:         time.Duration(maxI32(a.Spec.Policy.ErrorFreezeSeconds, 0)) * time.Second,
	}
	if p, ok := r.plannerByKey[key]; ok {
		if !limitsEqual(p, lim) {
			np, _ := policy.NewPlanner(lim)
			r.plannerByKey[key] = np
			return np
		}
		return p
	}
	np, _ := policy.NewPlanner(lim)
	r.plannerByKey[key] = np
	return np
}

func limitsEqual(p *policy.Planner, lim policy.Limits) bool {
	_ = p
	_ = lim
	return false
}

func round1(x float64) float64 {
	return math.Round(x*10) / 10
}

func orDefault[T ~string](v T, def T) T {
	if len(v) == 0 {
		return def
	}
	return v
}
func ptrI32(v int32) *int32 {
	return &v
}
func maxI32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
