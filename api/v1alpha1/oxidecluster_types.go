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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// Important: Run "make" to regenerate code after modifying this file

const (
	// ClusterFinalizer allows ReconcileOxideCluster to clean up resources associated with
	// OxideCluster before removing it from the apiserver.
	ClusterFinalizer = "oxidecluster.infrastructure.cluster.x-k8s.io"
)

// OxideClusterSpec defines the desired state of OxideCluster
type OxideClusterSpec struct {
	// The Oxide project where the cluster is installed.
	Project string `json:"project"`

	// Network specifications
	// +optional
	NetworkSpec Network `json:"networkSpec,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the
	// control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// OxideClusterStatus defines the observed state of OxideCluster
type OxideClusterStatus struct {
	// Ready indicates that the cluster is ready.
	// +optional
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// FailureDomains is a list of failure domains that CAPI will spread across machines.
	// +optional
	FailureDomains clusterv1.FailureDomains `json:"failureDomains,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// OxideCluster is the Schema for the oxideclusters API
type OxideCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OxideClusterSpec   `json:"spec,omitempty"`
	Status OxideClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// OxideClusterList contains a list of OxideCluster
type OxideClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OxideCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OxideCluster{}, &OxideClusterList{})
}
