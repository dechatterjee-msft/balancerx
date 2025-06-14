package util

import (
	balancerxv1alpha1 "balancerx/api/v1alpha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func HashSpec(sp balancerxv1alpha1.BalancerPolicySpec) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", sp.SourceNamespace, sp.GVR, sp.Balancer)))
	return hex.EncodeToString(h[:8])
}

func MakeSelector(name, gvr string) string {
	h := sha256.Sum256([]byte(name + "|" + gvr))
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
