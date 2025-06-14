package controller

import (
	"balancerx/internal/balancer"
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Dispatcher struct {
	client.Client
	GVK      schema.GroupVersionKind
	Balancer balancer.Balancer
}

func (d *Dispatcher) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithName("Dispatcher").WithValues("namespace", req.Namespace, "name", req.Name)
	src := &unstructured.Unstructured{}
	src.SetGroupVersionKind(d.GVK)
	err := d.Get(ctx, req.NamespacedName, src)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	annotations := src.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	targetNS, err := d.Balancer.Get(req.Name)
	if err != nil {
		logger.Error(err, "Failed to get target namespace from balancer")
		return reconcile.Result{}, err
	}
	tgt := src.DeepCopy()
	tgt.SetNamespace(targetNS)
	tgt.SetResourceVersion("")
	tgt.SetUID("")
	// labels for metrics/debugging
	labels := tgt.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["balancer/target"] = targetNS
	tgt.SetLabels(labels)
	tgt.SetAnnotations(map[string]string{"dispatched": "true"})
	if err := d.Create(ctx, tgt); err != nil {
		logger.Error(err, "Failed to create in target namespace", "namespace", targetNS)
		return reconcile.Result{}, err
	}
	if err := d.Delete(ctx, src); err != nil {
		logger.Error(err, "Failed to delete original object")
		return reconcile.Result{}, err
	}
	logger.Info("Dispatched CustomResource", "originalNamespace", req.Namespace, "name", req.Name, "targetNamespace", targetNS)
	return reconcile.Result{}, nil
}

func SetupDispatcher(mgr manager.Manager, gvr schema.GroupVersionResource, bal balancer.Balancer) error {
	u := &unstructured.Unstructured{}
	restMapper := mgr.GetRESTMapper()
	gvk, err := restMapper.KindFor(gvr)
	if err != nil {
		return err
	}
	kind := gvk.Kind
	u.SetGroupVersionKind(gvr.GroupVersion().WithKind(kind))
	pred := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetAnnotations()["dispatched"] != "true"
		},
		UpdateFunc:  func(e event.UpdateEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
	return builder.ControllerManagedBy(mgr).
		For(u, builder.WithPredicates(pred)).
		Complete(&Dispatcher{
			Client:   mgr.GetClient(),
			GVK:      gvk,
			Balancer: bal,
		})
}
