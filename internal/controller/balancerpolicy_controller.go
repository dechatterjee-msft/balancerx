package controller

import (
	balancerxv1alpha1 "balancerx/api/v1alpha1"
	"balancerx/internal/balancer"
	"balancerx/internal/util"
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sync"
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

const FinalizerName = "balancerx.io/finalizer"

const (
	CondReady     = "Ready"
	CondInvalid   = "InvalidSpec"
	CondCollision = "Collision"
)

//+kubebuilder:rbac:groups=balancerx.io,resources=balancerpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=balancerx.io,resources=balancerpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=balancerx.io,resources=balancerpolicies/finalizers,verbs=update

func (r *BalancerPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	//  Fetch object or handle delete ====================================
	var pol balancerxv1alpha1.BalancerPolicy
	if err := r.Get(ctx, req.NamespacedName, &pol); err != nil {
		if errors.IsNotFound(err) {
			r.stop(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if pol.Annotations == nil {
		pol.Annotations = map[string]string{}
	}
	if pol.ObjectMeta.DeletionTimestamp != nil {
		// ensure dispatcher is stopped
		r.stop(pol.Name)
		// remove finalizer, then update
		if controllerutil.ContainsFinalizer(&pol, FinalizerName) {
			controllerutil.RemoveFinalizer(&pol, FinalizerName)
			if err := r.Update(ctx, &pol); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
	// ensure finalizer present for future graceful delete
	if !controllerutil.ContainsFinalizer(&pol, FinalizerName) {
		controllerutil.AddFinalizer(&pol, FinalizerName)
		if err := r.Update(ctx, &pol); err != nil {
			return ctrl.Result{}, err
		}
		// requeue to process rest on next pass
		//	return ctrl.Result{}, nil
	}

	// -----------------------------------------------------------------
	//  Validate spec (GVR format & collisions)
	// -----------------------------------------------------------------
	specHash := util.HashSpec(pol.Spec)
	if existing := pol.Annotations["balancerx/spec-hash"]; existing == specHash && r.isRunning(pol.Name) {
		logger.Info("skipping reconcile, spec unchanged and dispatcher running")
		return ctrl.Result{
			Requeue: false,
		}, nil
	}

	if cond := r.validateSpec(ctx, &pol); cond != nil {
		util.SetCondition(&pol.Status.Conditions, CondInvalid, metav1.ConditionTrue, cond.Reason, cond.Message, pol.Generation)
		pol.Status.OverallStatus = "Failed"
		logger.Info("validation error occurred", "reason", cond.Reason, "message", cond.Message)
		_ = r.Status().Update(ctx, &pol)
		r.stop(pol.Name)
		return ctrl.Result{
			Requeue: false,
		}, nil
	}
	util.ClearCondition(&pol.Status.Conditions, CondInvalid)

	// -----------------------------------------------------------------
	//  Validate balancer type
	// -----------------------------------------------------------------
	balancerImpl, err := balancer.NewLoadBalancingFactory(pol.Spec.Balancer)
	if err != nil {
		pol.Status.OverallStatus = "Failed"
		util.SetCondition(&pol.Status.Conditions, CondInvalid, metav1.ConditionTrue, "BadBalancer", err.Error(), pol.Generation)
		_ = r.Status().Update(ctx, &pol)
		r.stop(pol.Name)
		return ctrl.Result{}, nil
	}

	// -----------------------------------------------------------------
	// Ensure workerSelector in status (first run)
	// -----------------------------------------------------------------
	if pol.Status.WorkerSelector == "" {
		pol.Status.WorkerSelector = util.MakeSelector(pol.Spec.GVR)
		pol.Status.OverallStatus = "Pending"
		if err := r.Status().Update(ctx, &pol); err != nil {
			pol.Status.OverallStatus = "Failed"
			logger.Error(err, "unable to update status")
			return ctrl.Result{}, err
		}
	}

	// -----------------------------------------------------------------
	// (Re)start dispatcher
	// -----------------------------------------------------------------

	r.stop(pol.Name)
	ctxSub, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.cancel == nil {
		r.cancel = map[string]context.CancelFunc{}
	}
	r.cancel[pol.Name] = cancel
	r.mu.Unlock()

	cfg := Config{
		GVR:                 util.ParseGVR(pol.Spec.GVR),
		SourceNS:            pol.Spec.SourceNamespace,
		WorkerSelector:      pol.Status.WorkerSelector,
		LoadBalancingPolicy: pol.Spec.Balancer,
	}

	if err := r.mgr.Add(manager.RunnableFunc(func(_ context.Context) error {
		return Run(ctxSub, r.mgr, cfg, balancerImpl)
	})); err != nil {
		logger.Error(err, "unable to start load balancer dispatcher")
		pol.Status.OverallStatus = "Failed"
		util.SetCondition(&pol.Status.Conditions, CondReady, metav1.ConditionFalse, "StartFailed", err.Error(), pol.Generation)
		_ = r.Status().Update(ctx, &pol)
		return ctrl.Result{
			Requeue: false,
		}, nil
	}

	// -----------------------------------------------------------------
	// Persist annotation & Ready condition
	// -----------------------------------------------------------------

	pol.Annotations["balancerx/spec-hash"] = specHash
	if err := r.Update(ctx, &pol); err != nil {
		return ctrl.Result{}, err
	}
	pol.Status.OverallStatus = "Running"
	util.SetCondition(&pol.Status.Conditions, CondReady, metav1.ConditionTrue, "DispatcherRunning", "dispatcher active", pol.Generation)
	_ = r.Status().Update(ctx, &pol)

	logger.Info("dispatcher (re)started", "policy", pol.Name)
	return ctrl.Result{
		Requeue: false,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BalancerPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.mgr = mgr
	return ctrl.NewControllerManagedBy(mgr).
		For(&balancerxv1alpha1.BalancerPolicy{}).WithEventFilter(
		predicate.Funcs{
			CreateFunc: func(createEvent event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool { return true }},
	).Complete(r)
}

func (r *BalancerPolicyReconciler) stop(name string) {
	r.mu.Lock()
	if c, ok := r.cancel[name]; ok {
		c()
		delete(r.cancel, name)
	}
	r.mu.Unlock()
}

func (r *BalancerPolicyReconciler) validateSpec(ctx context.Context, pol *balancerxv1alpha1.BalancerPolicy) *metav1.Condition {
	// validate GVR string format
	if !util.IsValidGVR(pol.Spec.GVR) {
		return &metav1.Condition{Reason: "BadGVR", Message: "GVR must be group/version/resource"}
	}

	// determine worker namespaces selected by THIS policy
	workerSel := util.MakeSelector(pol.Spec.GVR)
	workerSet, _ := util.ListWorkerNS(ctx, r.Client, workerSel)

	// scan all other BalancerPolicies
	var list balancerxv1alpha1.BalancerPolicyList
	if err := r.List(ctx, &list); err != nil {
		return &metav1.Condition{Reason: "ListError", Message: err.Error()}
	}

	for _, p := range list.Items {
		if p.Name == pol.Name {
			continue
		}
		// give priority to older policies
		if p.CreationTimestamp.Before(&pol.CreationTimestamp) {
			if p.Spec.SourceNamespace == pol.Spec.SourceNamespace {
				return &metav1.Condition{Type: CondCollision, Reason: "SourceNamespaceInUse", Message: fmt.Sprintf("namespace %s already used by %s", p.Spec.SourceNamespace, p.Name)}
			}
			if p.Status.WorkerSelector != "" {
				otherSet, _ := util.ListWorkerNS(ctx, r.Client, p.Status.WorkerSelector)
				if workerSet.Intersection(otherSet).Len() > 0 {
					return &metav1.Condition{Type: CondCollision, Reason: "WorkerOverlap", Message: "worker namespace overlap"}
				}
			}
		}
	}
	return nil
}

// ----- utilities ------------------------------------------------------------
func (r *BalancerPolicyReconciler) isRunning(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancel[name]
	return ok
}
