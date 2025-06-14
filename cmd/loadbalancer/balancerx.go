package main

import (
	"balancerx/internal/balancer"
	"balancerx/internal/controller"
	"context"
	"flag"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"os/signal"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"syscall"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var (
		group                          = flag.String("group", "mygroup.io", "API group (required)")
		version                        = flag.String("version", "v1alpha1", "API version (required)")
		resource                       = flag.String("resource", "examplejobs", "resource plural name (required)")
		loadBalancingPolicy            = flag.String("policy", "roundrobin", "balancer policy: ringhash|roundrobin|leastconn (default ringhash)")
		loadBalancingNamespaceSelector = flag.String("namespace-selector", "balancerx/worker=true", "Namespace selector for load balancing (optional, default is all namespaces)")
	)
	flag.Parse()
	if *group == "" || *version == "" || *resource == "" {
		flag.Usage()
		os.Exit(1)
	}
	gvr := schema.GroupVersionResource{Group: *group, Version: *version, Resource: *resource}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "unable to get config")
		return
	}
	opts := zap.Options{
		Development: true,
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		LeaderElection:   true,
		LeaderElectionID: "68f3f7e.balancerx.microsoft.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)
	go func() {
		<-signalChan // Wait for shutdown signal
		setupLog.Info("received terminate signal, shutting down manager")
		cancel() // Cancel context to notify workers
	}()
	go func() {
		<-mgr.Elected() // waits for leader election and cache to start
		setupLog.Info("manager elected as leader, starting controllers")
		loadBalancer, err := balancer.NewLoadBalancingFactory(*loadBalancingPolicy)
		if err != nil {
			setupLog.Error(err, "unable to create load balancer factory")
			return
		}
		if err := controller.SetupDispatcher(mgr, gvr, loadBalancer); err != nil {
			setupLog.Error(err, "unable to setup dispatcher")
			return
		}
		if err = controller.SetupNamespaceDiscovery(ctx, mgr, *loadBalancingNamespaceSelector, loadBalancer, mgr.GetLogger()); err != nil {
			setupLog.Error(err, "unable to setup namespace discovery")
			return
		}
	}()

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Manager exited with error")
		panic(err)
	}
}
