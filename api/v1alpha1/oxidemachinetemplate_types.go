/*
Copyright 2026.

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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// OxideMachineTemplateSpec defines the desired state of OxideMachineTemplate
type OxideMachineTemplateSpec struct {
	Template OxideMachineTemplateResource `json:"template"`
}

// OxideMachineTemplateResource describes the data needed to create an OxideMachine from a template.
type OxideMachineTemplateResource struct {
	// ObjectMeta is the labels and annotations to apply to the OxideMachines created from this
	// template.
	// +optional
	ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec is the specification of the desired behavior of the machine.
	Spec OxideMachineSpec `json:"spec"`
}

// // OxideMachineTemplateStatus defines the observed state of OxideMachineTemplate.
// type OxideMachineTemplateStatus struct {
// 	// conditions represent the current state of the OxideMachineTemplate resource.
// 	// Each condition has a unique type and reflects the status of a specific aspect of the
// resource.
// 	//
// 	// Standard condition types include:
// 	// - "Available": the resource is fully functional
// 	// - "Progressing": the resource is being created or updated
// 	// - "Degraded": the resource failed to reach or maintain its desired state
// 	//
// 	// The status of each condition is one of True, False, or Unknown.
// 	// +listType=map
// 	// +listMapKey=type
// 	// +optional
// 	Conditions []metav1.Condition `json:"conditions,omitempty"`
// }

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta1=v1alpha1"
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"
// OxideMachineTemplate is the Schema for the oxidemachinetemplates API
type OxideMachineTemplate struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of OxideMachineTemplate
	// +required
	Spec OxideMachineTemplateSpec `json:"spec"`

	// TODO: Implement template status for the cluster autoscaler.
	// // status defines the observed state of OxideMachineTemplate
	// // +optional
	// Status OxideMachineTemplateStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OxideMachineTemplateList contains a list of OxideMachineTemplate
type OxideMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OxideMachineTemplate `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &OxideMachineTemplate{}, &OxideMachineTemplateList{})
}
