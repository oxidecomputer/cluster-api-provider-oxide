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

const ClusterFinalizer = "oxidecluster.infrastructure.cluster.x-k8s.io"

// OxideClusterSpec defines the desired state of OxideCluster
type OxideClusterSpec struct {
	// Project is the name of the Oxide project in which the cluster will reside. The project should
	// be created out of band and isn't managed by the controller.
	Project string `json:"project"`

	// VPC is the name of the Oxide VPC in which the cluster will reside. The VPC should be created
	// out of band and isn't managed by the controller.
	VPC string `json:"vpc"`

	// Subnet is the name of the Oxide subnet in which the cluster will reside. The subnet should be
	// created out of band and isn't managed by the controller.
	Subnet string `json:"subnet"`

	// CredentialsRef is the reference to the Secret resource containing Oxide API credentials. The
	// Secret must contain `oxide-host` and `oxide-token` fields to authenticate to the API.
	CredentialsRef SecretReference `json:"credentialsRef"`

	// ControlPlaneEndpoint represents the host and port of the cluster's control plane. If
	// ControlPlaneEndpoint.Host is set to an IP address by the user, provision a matching floating
	// IP address. Otherwise, choose a floating IP based on IPPool and IPType.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// IPPool is the Oxide IP pool to use for the cluster's floating IP. This field is not used if
	// ControlPlaneEndpoint.Host is set.
	// +optional
	IPPool string `json:"ipPool,omitempty"`

	// IPType is the IP type (v4 or v6) to use for the cluster's floating IP. This field is not used
	// if ControlPlaneEndpoint.Host or IPPool is set.
	// +optional
	// +kubebuilder:validation:Enum=v4;v6
	IPType string `json:"ipType,omitempty"`
}

// SecretReference is a reference to a Secret containing Oxide credentials. Adapted from
// corev1.SecretReference, with both fields required.
type SecretReference struct {
	// Name is the name of the Secret resource.
	Name string `json:"name"`
	// Namespace is the namespace of the Secret resource.
	Namespace string `json:"namespace"`
}

// OxideClusterStatus defines the observed state of OxideCluster.
type OxideClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the OxideCluster resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the
	// resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Initialization provides observations of the OxideCluster initialization process.
	// NOTE: Fields in this struct are part of the Cluster API contract and are used to orchestrate
	// initial Cluster provisioning.
	// +optional
	Initialization OxideClusterInitializationStatus `json:"initialization,omitempty"`
}

// OxideClusterInitializationStatus provides observations of the OxideCluster initialization
// process.
// +kubebuilder:validation:MinProperties=1
type OxideClusterInitializationStatus struct {
	// Provisioned is the provisioning state of the cluster. This field is set to true once the
	// Oxide floating IP address is created. Part of the CAPI contract.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Attachment",type="string",JSONPath=".status.conditions[?(@.type=='FloatingIPAttached')].reason"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// OxideCluster is the Schema for the oxideclusters API
type OxideCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of OxideCluster
	// +required
	Spec OxideClusterSpec `json:"spec"`

	// status defines the observed state of OxideCluster
	// +optional
	Status OxideClusterStatus `json:"status,omitzero"`
}

func (c *OxideCluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

func (c *OxideCluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// OxideClusterList contains a list of OxideCluster
type OxideClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OxideCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OxideCluster{}, &OxideClusterList{})
}
