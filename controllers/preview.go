package controllers

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/vshulcz/cpu-autoscaler/api/v1alpha1"
	"github.com/vshulcz/cpu-autoscaler/internal/metrics"
	gcli "github.com/vshulcz/cpu-autoscaler/internal/metrics/golectra"
	"github.com/vshulcz/cpu-autoscaler/internal/plan/policy"
	"k8s.io/apimachinery/pkg/types"
)

type PreviewRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type PreviewResponse struct {
	DTSeconds       int32   `json:"dtSeconds"`
	CPUNow          float64 `json:"cpuNow"`
	CPUSmooth       float64 `json:"cpuSmooth"`
	TargetCPU       float64 `json:"targetCPU"`
	CurrentReplicas int32   `json:"currentReplicas"`
	DesiredRaw      int32   `json:"desiredRaw"`
	Applied         int32   `json:"applied"`
	Limits          struct {
		Min         int32 `json:"min"`
		Max         int32 `json:"max"`
		MaxStepUp   int32 `json:"maxStepUp"`
		MaxStepDown int32 `json:"maxStepDown"`
	} `json:"limits"`
	Reason string `json:"reason"`
}

func (r *CPUBasedAutoscalerReconciler) PreviewPlan(ctx context.Context, in PreviewRequest) (PreviewResponse, error) {
	var res PreviewResponse
	if in.Namespace == "" || in.Name == "" {
		return res, fmt.Errorf("namespace and name are required")
	}
	var a v1.CPUBasedAutoscaler
	if err := r.Get(ctx, clientKey(in.Namespace, in.Name), &a); err != nil {
		return res, err
	}

	now := r.Clock.Now()
	targetNS := a.Spec.TargetRef.Namespace
	if targetNS == "" {
		targetNS = a.Namespace
	}
	cur, _, err := r.getCurrentReplicas(ctx, targetNS, a.Spec.TargetRef)
	if err != nil {
		return res, fmt.Errorf("get target replicas: %w", err)
	}

	var cpuNow float64
	validMetrics := true
	if a.Spec.Source.Type != v1.SourceGolectra || a.Spec.Source.Golectra == nil {
		validMetrics = false
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
		} else {
			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			snap, err := cli.Snapshot(reqCtx)
			cancel()
			if err == nil {
				if avg, err := metrics.AggregateCPUPercent(snap.Gauges, orDefault(goSrc.CPUKeyPrefix, "CPUutilization")); err == nil {
					cpuNow = avg
				} else {
					validMetrics = false
				}
			} else {
				validMetrics = false
			}
		}
	}

	key := fmt.Sprintf("%s/%s", a.Namespace, a.Name)
	se := r.ensureSES(key, float64(a.Spec.Control.Alpha), int(a.Spec.Control.WarmupPoints))
	clone := se.CloneState()
	sePrev := &clone
	if !sePrev.Warmed() && a.Status.CPUSmoothPercent > 0 {
		sePrev.Prime(a.Status.CPUSmoothPercent)
	}
	var cpuSmooth float64
	if validMetrics {
		cpuSmooth = sePrev.Observe(cpuNow)
	} else {
		cpuSmooth, _ = sePrev.Value()
	}
	warmed := sePrev.Warmed()

	pl := r.ensurePlanner(key, a)
	plc := pl.Clone()
	dec := plc.Plan(key, policy.Inputs{
		Now:                now,
		CurrentReplicas:    cur,
		ForecastCPUPercent: cpuSmooth,
		TargetCPUPercent:   float64(a.Spec.Control.TargetCPUPercent),
		ValidMetrics:       validMetrics,
		Warmed:             warmed,
	})

	res.DTSeconds = a.Spec.Control.DTSeconds
	res.CPUNow = round1(cpuNow)
	res.CPUSmooth = round1(cpuSmooth)
	res.TargetCPU = float64(a.Spec.Control.TargetCPUPercent)
	res.CurrentReplicas = cur
	res.DesiredRaw = dec.DesiredRaw
	res.Applied = dec.Applied
	res.Limits.Min = dec.Limits.MinReplicas
	res.Limits.Max = dec.Limits.MaxReplicas
	res.Limits.MaxStepUp = dec.Limits.MaxStepUp
	res.Limits.MaxStepDown = dec.Limits.MaxStepDown
	switch dec.Reason {
	case policy.ReasonFreeze:
		res.Reason = string(v1.ReasonFreeze)
	case policy.ReasonMaxStep:
		res.Reason = string(v1.ReasonMaxStep)
	default:
		res.Reason = string(v1.ReasonForecast)
	}
	return res, nil
}

func clientKey(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}
