package controller

import (
	"balancerx/internal/balancer"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// SetupNamespaceDiscovery discovers namespaces matching the supplied
// label selector and keeps the supplied Balancer in sync by watching
// add/update/delete events. Call this *once* during startup *before*
// mgr.Start().
//
//	selector := "balancerx/worker=Hash(GVR + Name)"
//	if err := discovery.SetupNamespaceDiscovery(mgr, selector, bal, logger);
//
// The function:
//  1. Lists current namespaces that match the selector and adds them to bal.
//  2. Registers an informer handler to keep the balancer up‑to‑date.
//
// It is safe to call multiple times with the same selector (handlers are idempotent).

func SetupNamespaceDiscovery(ctx context.Context, mgr manager.Manager, selector string, bal balancer.Balancer, logger logr.Logger, group string) error {
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
	logger.Info("initial namespaces added to balancer", "count", len(nsList.Items), "group", group, "selector", selector)
	inf, err := mgr.GetCache().GetInformer(ctx, &corev1.Namespace{})
	if err != nil {
		return err
	}
	_, err = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if ctx.Err() != nil {
				return
			}
			ns := obj.(*corev1.Namespace)
			if ls.Matches(labels.Set(ns.Labels)) {
				bal.Add(ns.Name)
				logger.Info("namespace added (event)", "ns", ns.Name, "group", group, "selector", selector)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if ctx.Err() != nil {
				return
			}
			ns := obj.(*corev1.Namespace)
			if ls.Matches(labels.Set(ns.Labels)) {
				bal.Remove(ns.Name)
				logger.Info("namespace removed (event)", "ns", ns.Name, "group", group, "selector", selector)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if ctx.Err() != nil {
				return
			}
			oldNs := oldObj.(*corev1.Namespace)
			newNs := newObj.(*corev1.Namespace)
			oldMatch := ls.Matches(labels.Set(oldNs.Labels))
			newMatch := ls.Matches(labels.Set(newNs.Labels))
			switch {
			case !oldMatch && newMatch:
				bal.Add(newNs.Name)
				logger.Info("namespace label now matches; added", "ns", newNs.Name, "group", group, "selector", selector)
			case oldMatch && !newMatch:
				bal.Remove(newNs.Name)
				logger.Info("namespace label removed; removed", "ns", newNs.Name, "group", group, "selector", selector)
			}
		},
	})
	if err != nil {
		return err
	}
	return nil
}

func PeriodicNamespaceDiscovery(ctx context.Context, mgr manager.Manager, cfg Config, logger logr.Logger) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := CreateNInitialReplicasNamespaceDiscovery(ctx, mgr, cfg, logger)
				if err != nil {
					logger.Error(err, "Failed to reconcile namespaces")
				}
			}
		}
	}()
}

func CreateNInitialReplicasNamespaceDiscovery(ctx context.Context, mgr manager.Manager, cfg Config, logger logr.Logger) error {
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}
	// Setting the owner reference to the BalancerPolicy
	balancerPolicy := &unstructured.Unstructured{}
	balancerPolicy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "balancerx.io",
		Version: "v1alpha1",
		Kind:    "BalancerPolicy",
	})
	k8sClient := mgr.GetClient()
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: cfg.PolicyName}, balancerPolicy); err != nil {
		return fmt.Errorf("failed to get BalancerPolicy: %w", err)
	}

	ownerRef := v1.OwnerReference{
		APIVersion:         "balancerx.io/v1alpha1",
		Kind:               "BalancerPolicy",
		Name:               balancerPolicy.GetName(),
		UID:                balancerPolicy.GetUID(),
		Controller:         pointer.Bool(true),
		BlockOwnerDeletion: pointer.Bool(true),
	}

	hash := strings.Split(cfg.WorkerSelector, "=")[1]
	for i := 1; i <= cfg.Replicas; i++ {
		nsName := fmt.Sprintf("%s-%s", cfg.SourceNS, hashPrefix(fmt.Sprintf("%s-%d", hash, i)))
		_, err := clientset.CoreV1().Namespaces().Get(ctx, nsName, v1.GetOptions{})
		if errors.IsNotFound(err) {
			logger.Info("Creating namespace", "name", nsName)
			_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"balancer/managed": "true",
						"balancer/source":  cfg.SourceNS,
						"balancer/worker":  hash,
					},
					OwnerReferences: []v1.OwnerReference{ownerRef},
				},
			}, v1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace %s: %w", nsName, err)
			}
		} else if err != nil {
			return fmt.Errorf("error checking namespace %s: %w", nsName, err)
		}
	}

	return nil
}

func hashPrefix(name string) string {
	hash := sha1.Sum([]byte(name))
	return hex.EncodeToString(hash[:3]) // ~6 characters
}
