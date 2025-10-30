/*
Copyright 2025 vshulcz.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"net/http"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"go.uber.org/zap/zapcore"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	autoscalev1alpha1 "github.com/vshulcz/cpu-autoscaler/api/v1alpha1"
	"github.com/vshulcz/cpu-autoscaler/controllers"
	"github.com/vshulcz/cpu-autoscaler/internal/httpapi"
	// +kubebuilder:scaffold:imports
)

type appConfig struct {
	MetricsAddr                                      string
	MetricsCertPath, MetricsCertName, MetricsCertKey string
	WebhookCertPath, WebhookCertName, WebhookCertKey string
	EnableLeaderElection                             bool
	LeaderElResourceType                             string
	Workers                                          int
	HTTPBind                                         string
	LogLevel                                         string
	ProbeAddr                                        string
	SecureMetrics                                    bool
	EnableHTTP2                                      bool
}

func parseFlags() (appConfig, *zap.Options) {
	var cfg appConfig
	var opts zap.Options

	flag.StringVar(&cfg.MetricsAddr, "metrics-bind-address", "0",
		"The address the metrics endpoint binds to. Use :8443 for HTTPS or :8080 for HTTP, or 0 to disable.")
	flag.StringVar(&cfg.ProbeAddr, "health-probe-bind-address", ":8081", "Probe endpoint bind address.")
	flag.BoolVar(&cfg.EnableLeaderElection, "leader-elect", false, "Enable leader election.")
	flag.StringVar(&cfg.LeaderElResourceType, "leader-election-resource-lock",
		resourcelock.LeasesResourceLock, "Leader election resource lock type.")
	flag.IntVar(&cfg.Workers, "workers", 2, "Concurrent reconciles per controller.")
	flag.StringVar(&cfg.HTTPBind, "http-bind", ":8080", "Internal HTTP bind (debug/api).")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "Log level: info|debug.")
	flag.BoolVar(&cfg.SecureMetrics, "metrics-secure", true, "Serve metrics via HTTPS.")
	flag.StringVar(&cfg.WebhookCertPath, "webhook-cert-path", "", "Dir with webhook TLS cert/key.")
	flag.StringVar(&cfg.WebhookCertName, "webhook-cert-name", "tls.crt", "Webhook cert file name.")
	flag.StringVar(&cfg.WebhookCertKey, "webhook-cert-key", "tls.key", "Webhook key file name.")
	flag.StringVar(&cfg.MetricsCertPath, "metrics-cert-path", "", "Dir with metrics TLS cert/key.")
	flag.StringVar(&cfg.MetricsCertName, "metrics-cert-name", "tls.crt", "Metrics cert file name.")
	flag.StringVar(&cfg.MetricsCertKey, "metrics-cert-key", "tls.key", "Metrics key file name.")
	flag.BoolVar(&cfg.EnableHTTP2, "enable-http2", false, "Enable HTTP/2 for metrics and webhook servers.")

	opts = zap.Options{}
	opts.BindFlags(flag.CommandLine)

	flag.Parse()
	return cfg, &opts
}

func buildLogger(level string, opts *zap.Options) logr.Logger {
	var lvl zapcore.Level
	if level == "debug" {
		lvl = zapcore.DebugLevel
	} else {
		lvl = zapcore.InfoLevel
	}
	opts.Level = lvl
	logger := zap.New(zap.UseFlagOptions(opts))
	ctrl.SetLogger(logger)
	return logger
}

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(autoscalev1alpha1.AddToScheme(s))
	// +kubebuilder:scaffold:scheme
	return s
}

func tlsOptions(enableHTTP2 bool, log logr.Logger) []func(*tls.Config) {
	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	var tlsOpts []func(*tls.Config)
	if !enableHTTP2 {
		disableHTTP2 := func(c *tls.Config) {
			log.Info("disabling http/2")
			c.NextProtos = []string{"http/1.1"}
		}
		tlsOpts = append(tlsOpts, disableHTTP2)
	}
	return tlsOpts
}

func setupWebhook(cfg appConfig, tlsOpts []func(*tls.Config), log logr.Logger) webhook.Server {
	// Initial webhook TLS options
	opts := webhook.Options{TLSOpts: tlsOpts}
	if len(cfg.WebhookCertPath) > 0 {
		log.Info("initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", cfg.WebhookCertPath, "webhook-cert-name",
			cfg.WebhookCertName, "webhook-cert-key", cfg.WebhookCertKey)
		opts.CertDir = cfg.WebhookCertPath
		opts.CertName = cfg.WebhookCertName
		opts.KeyName = cfg.WebhookCertKey
	}
	return webhook.NewServer(opts)
}

func setupMetrics(cfg appConfig, tlsOpts []func(*tls.Config), log logr.Logger) metricsserver.Options {
	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	opts := metricsserver.Options{
		BindAddress:   cfg.MetricsAddr,
		SecureServing: cfg.SecureMetrics,
		TLSOpts:       tlsOpts,
	}
	if cfg.SecureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		opts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(cfg.MetricsCertPath) > 0 {
		log.Info("initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", cfg.MetricsCertPath, "metrics-cert-name",
			cfg.MetricsCertName, "metrics-cert-key", cfg.MetricsCertKey)
		opts.CertDir = cfg.MetricsCertPath
		opts.CertName = cfg.MetricsCertName
		opts.KeyName = cfg.MetricsCertKey
	}
	return opts
}

func newManager(
	scheme *runtime.Scheme,
	metricsOpts metricsserver.Options,
	webhookSrv webhook.Server,
	cfg appConfig,
) (manager.Manager, error) {
	return ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                     scheme,
		Metrics:                    metricsOpts,
		WebhookServer:              webhookSrv,
		HealthProbeBindAddress:     cfg.ProbeAddr,
		LeaderElection:             cfg.EnableLeaderElection,
		LeaderElectionID:           "cpu-autoscaler.example.com",
		LeaderElectionResourceLock: cfg.LeaderElResourceType,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
}

func setupProbes(mgr manager.Manager) error {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}
	return nil
}

func setupReconciler(
	mgr manager.Manager,
	workers int,
	log logr.Logger,
) (*controllers.CPUBasedAutoscalerReconciler, error) {
	rec := mgr.GetEventRecorderFor("cpu-autoscaler")
	r := controllers.NewCPUBAReconciler(mgr.GetClient(), mgr.GetScheme(), rec, nil)
	if err := r.SetupWithManager(mgr, workers); err != nil {
		return nil, err
	}
	log.Info("controller set up", "workers", workers)
	return r, nil
}

func setupHTTPServer(
	mgr manager.Manager,
	addr string,
	reconciler *controllers.CPUBasedAutoscalerReconciler,
	log logr.Logger,
) error {
	srv := httpapi.New(httpapi.Options{
		Addr: addr,
		Preview: func(ctx context.Context, body []byte) (int, []byte) {
			var req controllers.PreviewRequest
			if err := json.Unmarshal(body, &req); err != nil || req.Namespace == "" || req.Name == "" {
				resp, _ := json.Marshal(map[string]string{"error": "bad request"})
				return http.StatusBadRequest, resp
			}
			out, err := reconciler.PreviewPlan(ctx, req)
			if err != nil {
				resp, _ := json.Marshal(map[string]string{"error": err.Error()})
				return http.StatusInternalServerError, resp
			}
			resp, _ := json.Marshal(out)
			return http.StatusOK, resp
		},
		ReadyCheck: func() bool {
			return mgr.GetCache().WaitForCacheSync(context.TODO())
		},
	})
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		log.Info("http server starting", "bind", addr)
		return srv.Start(ctx)
	}))
}

func main() {
	cfg, zapOpts := parseFlags()

	logger := buildLogger(cfg.LogLevel, zapOpts)
	setupLog := logger.WithName("setup")

	scheme := buildScheme()

	tlsOpts := tlsOptions(cfg.EnableHTTP2, setupLog)
	webhookSrv := setupWebhook(cfg, tlsOpts, setupLog)
	metricsOpts := setupMetrics(cfg, tlsOpts, setupLog)

	mgr, err := newManager(scheme, metricsOpts, webhookSrv, cfg)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := setupProbes(mgr); err != nil {
		setupLog.Error(err, "unable to set up health/ready probes")
		os.Exit(1)
	}

	reconciler, err := setupReconciler(mgr, cfg.Workers, setupLog)
	if err != nil {
		setupLog.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	if err := setupHTTPServer(mgr, cfg.HTTPBind, reconciler, setupLog); err != nil {
		setupLog.Error(err, "unable to start http server")
		os.Exit(1)
	}

	setupLog.Info("starting manager",
		"metrics", cfg.MetricsAddr,
		"probes", cfg.ProbeAddr,
		"leaderElect", cfg.EnableLeaderElection,
		"workers", cfg.Workers,
		"httpBind", cfg.HTTPBind,
	)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
