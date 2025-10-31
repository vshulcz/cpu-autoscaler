package controllers

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	v1 "github.com/vshulcz/cpu-autoscaler/api/v1alpha1"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    = runtime.NewScheme()
	ctx       = context.Background()
)

func TestMain(m *testing.M) {
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Printf("add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		log.Printf("add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		log.Printf("add corev1 scheme: %v", err)
	}
	if err := autoscalingv2.AddToScheme(scheme); err != nil {
		log.Printf("add autoscalingv2 scheme: %v", err)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		log.Printf("add v1 scheme: %v", err)
	}

	crdDir := filepath.Join("..", "config", "crd", "bases")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdDir},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		panic(err)
	}
	os.Exit(code)
}
