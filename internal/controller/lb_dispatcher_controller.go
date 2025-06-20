package controller

import (
	"balancerx/internal/balancer"
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Config struct {
	Group               string
	SourceNS            string
	WorkerSelector      string
	LoadBalancingPolicy string
	Replicas            int
	PolicyName          string
}

type LoadBalancingDispatcher struct {
	client.Client
	GVK      schema.GroupVersionKind
	Balancer balancer.Balancer
	Group    string
	Scheme   *runtime.Scheme
}

var (
	setupLog = ctrl.Log.WithName("balancer-setup")
)

func (d *LoadBalancingDispatcher) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithName("LoadBalancingDispatcher").WithValues("namespace", req.Namespace, "name", req.Name)
	src := &unstructured.Unstructured{}
	src.SetGroupVersionKind(d.GVK)
	err := d.Get(ctx, req.NamespacedName, src)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	original := src.DeepCopy()
	annotations := src.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	targetNS, err := d.Balancer.Get(req.Name)
	if err != nil {
		logger.Error(err, "Failed to get target namespace from balancer")
		return reconcile.Result{}, err
	}
	src.SetUID("")
	// labels for metrics/debugging
	labels := src.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["balancer/target"] = targetNS
	src.SetLabels(labels)
	src.SetAnnotations(map[string]string{"dispatched": "true"})
	pathData := client.MergeFrom(original)
	if err := d.Patch(ctx, src, pathData); err != nil {
		logger.Error(err, "Failed to label the target worker namespace", "namespace", targetNS)
		return reconcile.Result{}, err
	}
	logger.Info("Dispatched CustomResource", "originalNamespace", req.Namespace, "name", req.Name, "targetNamespaceController", targetNS)
	return reconcile.Result{}, nil
}

// SetupDispatcherGroup sets up a controller for a specific group and its resources.
func SetupDispatcherGroup(ctx context.Context, mgr manager.Manager, group string, bal balancer.Balancer) error {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	apiResourceLists, err := discoveryClient.ServerPreferredResources()
	if err != nil {
		return err
	}

	for _, apiResourceList := range apiResourceLists {
		gv, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil || gv.Group != group {
			continue
		}
		for _, res := range apiResourceList.APIResources {
			if res.Namespaced {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(gv.WithKind(res.Kind))
				pred := predicate.Funcs{
					CreateFunc: func(e event.CreateEvent) bool {
						return e.Object.GetAnnotations()["dispatched"] != "true"
					},
					UpdateFunc:  func(e event.UpdateEvent) bool { return false },
					DeleteFunc:  func(e event.DeleteEvent) bool { return false },
					GenericFunc: func(e event.GenericEvent) bool { return false },
				}
				if err := builder.ControllerManagedBy(mgr).
					For(u, builder.WithPredicates(pred)).
					Complete(&LoadBalancingDispatcher{
						Client:   mgr.GetClient(),
						GVK:      u.GroupVersionKind(),
						Group:    group,
						Balancer: bal,
						Scheme:   mgr.GetScheme(),
					}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func Run(ctx context.Context, mgr manager.Manager, cfg Config, loadBalancer balancer.Balancer) error {
	logger := log.FromContext(ctx)
	logger.Info("start BalancerX...", "group", cfg.Group)

	if err := SetupDispatcherGroup(ctx, mgr, cfg.Group, loadBalancer); err != nil {
		logger.Error(err, "unable to setup dispatcher")
		return nil
	}
	if err := CreateNInitialReplicasNamespaceDiscovery(ctx, mgr, cfg, logger); err != nil {
		logger.Error(err, "unable to create initial replicas namespace discovery")
	}
	if err := SetupNamespaceDiscovery(ctx, mgr, cfg.WorkerSelector, loadBalancer, mgr.GetLogger(), cfg.Group); err != nil {
		logger.Error(err, "unable to setup namespace discovery")
		return nil
	}
	PeriodicNamespaceDiscovery(ctx, mgr, cfg, logger)
	<-ctx.Done()
	logger.Info("stopping BalancerX....", "group", cfg.Group)
	return nil
}
