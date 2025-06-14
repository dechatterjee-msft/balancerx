package controller

import (
	"balancerx/internal/balancer"
	"balancerx/internal/util"
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"strings"
	"sync"

	balancerxv1alpha1 "balancerx/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BalancerPolicyReconciler reconciles a BalancerPolicy object
// -----------------------------------------------------------------------------
// BalancerPolicyReconciler: one BalancerX per policy CR
// -----------------------------------------------------------------------------

type BalancerPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	mgr    ctrl.Manager
	mu     sync.Mutex
	cancel map[string]context.CancelFunc // active dispatchers
}

const (
	PolicyReadyCondition = "Ready"     // dispatcher running
	CollisionCondition   = "Collision" // src or worker overlap
	InvalidSpecCondition = "InvalidSpec"
)

//+kubebuilder:rbac:groups=balancerx.io,resources=balancerpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=balancerx.io,resources=balancerpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=balancerx.io,resources=balancerpolicies/finalizers,verbs=update

func (r *BalancerPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling BalancerPolicy", "name", req.Name, "namespace", req.Namespace)
	// Fetch the BalancerPolicy instance
	balancerPolicy := &balancerxv1alpha1.BalancerPolicy{}
	if err := r.Get(ctx, req.NamespacedName, balancerPolicy); err != nil {
		logger.Error(err, "Failed to get BalancerPolicy", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Check if it is duplicate policy and user is using the same spec and balancer is already running.
	specHash := util.HashSpec(balancerPolicy.Spec)
	if balancerPolicy.Annotations == nil {
		balancerPolicy.Annotations = map[string]string{}
	}
	// If hash unchanged AND dispatcher already running, skip.
	if existing, ok := balancerPolicy.Annotations["balancerx/spec-hash"]; ok {
		if existing == specHash {
			r.mu.Lock()
			_, running := r.cancel[balancerPolicy.Name]
			r.mu.Unlock()
			if running {
				return ctrl.Result{}, nil // no change
			}
		}
	}
	// Validate selector and GVR
	labelSelector := util.MakeSelector(req.Name, balancerPolicy.Spec.GVR)
	_, err := labels.Parse(labelSelector)
	if err != nil {
		logger.Error(err, "invalid workerSelector")
		return ctrl.Result{
			Requeue: false,
		}, nil
	}

	gvrParts := strings.Split(balancerPolicy.Spec.GVR, "/")
	if len(gvrParts) != 3 {
		logger.Info("invalid GVR format", "gvr", balancerPolicy.Spec.GVR)
		return ctrl.Result{
			Requeue: false,
		}, nil
	}

	gvr := schema.GroupVersionResource{Group: gvrParts[0], Version: gvrParts[1], Resource: gvrParts[2]}
	lb, err := balancer.NewLoadBalancingFactory(balancerPolicy.Spec.Balancer)
	if err != nil {
		return ctrl.Result{
			Requeue: false,
		}, err
	}

	if balancerPolicy.Status.WorkerSelector == "" {
		// first time: just write selector and requeue
		balancerPolicy.Status.WorkerSelector = labelSelector
		if err = r.Status().Update(ctx, balancerPolicy); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // do not start dispatcher yet
	}

	// Restart dispatcher for this policy
	ctxSub, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.cancel == nil {
		r.cancel = make(map[string]context.CancelFunc)
	}
	r.cancel[balancerPolicy.Name] = cancel
	r.mu.Unlock()
	cfg := Config{
		GVR:                 gvr,
		SourceNS:            balancerPolicy.Spec.SourceNamespace,
		WorkerSelector:      labelSelector,
		LoadBalancingPolicy: balancerPolicy.Spec.Balancer,
	}
	if err := r.mgr.Add(manager.RunnableFunc(func(parentCtx context.Context) error {
		return Run(ctxSub, r.mgr, cfg, lb)
	})); err != nil {
		logger.Error(err, "failed to add runnable")
		return ctrl.Result{}, err
	}
	// Update the policy status with the new spec hash and worker selector
	balancerPolicy.Annotations["balancerx/spec-hash"] = specHash
	if err = r.Patch(ctx, balancerPolicy, nil); err != nil {
		return ctrl.Result{}, err
	}
	if balancerPolicy.Status.WorkerSelector != labelSelector {
		balancerPolicy.Status.WorkerSelector = labelSelector
		util.ClearCondition(&balancerPolicy.Status.Conditions, CollisionCondition)
		util.SetCondition(&balancerPolicy.Status.Conditions, PolicyReadyCondition,
			metav1.ConditionTrue, "DispatcherRunning",
			"dispatcher active",
			balancerPolicy.Generation)
		if err := r.Status().Update(ctx, balancerPolicy); err != nil {
			return ctrl.Result{}, err // retry
		}
	}
	return ctrl.Result{
		Requeue: false,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BalancerPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.mgr = mgr
	return ctrl.NewControllerManagedBy(mgr).
		For(&balancerxv1alpha1.BalancerPolicy{}).
		Complete(r)
}
