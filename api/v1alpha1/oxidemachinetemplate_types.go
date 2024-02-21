/*
Copyright 2024.

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

// OxideMachineTemplateSpec defines the desired state of OxideMachineTemplate
type OxideMachineTemplateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of OxideMachineTemplate. Edit oxidemachinetemplate_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// OxideMachineTemplateStatus defines the observed state of OxideMachineTemplate
type OxideMachineTemplateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// OxideMachineTemplate is the Schema for the oxidemachinetemplates API
type OxideMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OxideMachineTemplateSpec   `json:"spec,omitempty"`
	Status OxideMachineTemplateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// OxideMachineTemplateList contains a list of OxideMachineTemplate
type OxideMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OxideMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OxideMachineTemplate{}, &OxideMachineTemplateList{})
}
