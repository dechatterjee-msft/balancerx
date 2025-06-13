package controller

import (
	"balancerx/pkg/balancer"
	"context"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// SetupNamespaceDiscovery discovers namespaces matching the supplied
// label selector and keeps the supplied Balancer in sync by watching
// add/update/delete events. Call this *once* during startup *before*
// mgr.Start().
//
//	selector := "balancerx/worker=true"
//	if err := discovery.SetupNamespaceDiscovery(mgr, selector, bal, logger);
//
// The function:
//  1. Lists current namespaces that match the selector and adds them to bal.
//  2. Registers an informer handler to keep the balancer up‑to‑date.
//
// It is safe to call multiple times with the same selector (handlers are idempotent).
func SetupNamespaceDiscovery(ctx context.Context, mgr manager.Manager, selector string, bal balancer.Balancer, logger logr.Logger) error {
	ls, err := labels.Parse(selector)
	if err != nil {
		return err
	}
	// ---- initial list ----
	var nsList corev1.NamespaceList
	if err := mgr.GetClient().List(ctx, &nsList, client.MatchingLabelsSelector{Selector: ls}); err != nil {
		return err
	}
	for _, ns := range nsList.Items {
		bal.Add(ns.Name)
	}
	logger.Info("initial namespaces added to balancer", "count", len(nsList.Items))
	inf, err := mgr.GetCache().GetInformer(ctx, &corev1.Namespace{})
	if err != nil {
		return err
	}
	_, err = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ns := obj.(*corev1.Namespace)
			if ls.Matches(labels.Set(ns.Labels)) {
				bal.Add(ns.Name)
				logger.Info("namespace added (event)", "ns", ns.Name)
			}
		},
		DeleteFunc: func(obj interface{}) {
			ns := obj.(*corev1.Namespace)
			if ls.Matches(labels.Set(ns.Labels)) {
				bal.Remove(ns.Name)
				logger.Info("namespace removed (event)", "ns", ns.Name)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNs := oldObj.(*corev1.Namespace)
			newNs := newObj.(*corev1.Namespace)
			oldMatch := ls.Matches(labels.Set(oldNs.Labels))
			newMatch := ls.Matches(labels.Set(newNs.Labels))
			switch {
			case !oldMatch && newMatch:
				bal.Add(newNs.Name)
				logger.Info("namespace label now matches; added", "ns", newNs.Name)
			case oldMatch && !newMatch:
				bal.Remove(newNs.Name)
				logger.Info("namespace label removed; removed", "ns", newNs.Name)
			}
		},
	})
	if err != nil {
		return err
	}
	return nil
}
