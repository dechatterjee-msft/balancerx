package util

import (
	balancerxv1alpha1 "balancerx/api/v1alpha1"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

func HashSpec(sp balancerxv1alpha1.BalancerPolicySpec) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", sp.SourceNamespace, sp.Group, sp.Balancer)))
	return hex.EncodeToString(h[:8])
}

func MakeSelector(srcns, gvr string, customSelector []string) string {
	flattenSelector := ""
	if customSelector != nil {
		flattenSelector = strings.Join(customSelector, ",")
	}
	h := sha256.Sum256([]byte(gvr + "|" + flattenSelector + "|" + srcns))
	return fmt.Sprintf("balancer/worker=%s", hex.EncodeToString(h[:6])) // 12‑hex chars
}

func SetCondition(conds *[]metav1.Condition, t string, status metav1.ConditionStatus, reason, msg string, gen int64) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               t,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
	})
}

func ClearCondition(conds *[]metav1.Condition, t string) {
	meta.RemoveStatusCondition(conds, t)
}

// ListWorkerNS ----- helper: list namespaces matching selector ----------------------------
func ListWorkerNS(ctx context.Context, c client.Client, sel string) (sets.String, error) {
	selector, err := labels.Parse(sel)
	if err != nil {
		return nil, err
	}
	var nsList v1.NamespaceList
	if err := c.List(ctx, &nsList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, err
	}
	out := sets.NewString()
	for _, n := range nsList.Items {
		out.Insert(n.Name)
	}
	return out, nil
}

// IsValidGVR returns true if the string is in the form group/version/resource.
func IsValidGVR(s string) bool {
	parts := strings.Split(s, "/")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

// ParseGVR converts a string "group/version/resource" into schema.GroupVersionResource.
// It panics if format is invalid; use IsValidGVR first if you need safe check.
func ParseGVR(s string) schema.GroupVersionResource {
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		panic(fmt.Sprintf("invalid GVR: %s", s))
	}
	return schema.GroupVersionResource{Group: parts[0], Version: parts[1], Resource: parts[2]}
}
