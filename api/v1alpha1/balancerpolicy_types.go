/*
Copyright 2025.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BalancerPolicySpec defines the desired state of BalancerPolicy
// BalancerPolicySpec is the desired state.
type BalancerPolicySpec struct {
	// +kubebuilder:validation:MinLength=1
	SourceNamespace string `json:"sourceNamespace"`
	// Balancer strategy (ringhash|roundrobin|leastconn)
	// +kubebuilder:validation:Enum=ringhash;roundrobin;leastconn
	Balancer string `json:"balancer,omitempty"`
	Group    string `json:"group,omitempty"`
	// Custom Selector to select workers for the policy e.g. Subscription,ARM ID, RG.
	CustomSelector []string `json:"customSelector,omitempty"`
	Replicas       int      `json:"replicas,omitempty"`
}

// BalancerPolicyStatus defines the observed state of BalancerPolicy
type BalancerPolicyStatus struct {
	WorkerSelector string             `json:"workerSelector,omitempty"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
	OverallStatus  string             `json:"overallStatus,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// BalancerPolicy is the Schema for the balancerpolicies API
type BalancerPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BalancerPolicySpec   `json:"spec,omitempty"`
	Status BalancerPolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BalancerPolicyList contains a list of BalancerPolicy
type BalancerPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BalancerPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BalancerPolicy{}, &BalancerPolicyList{})
}
