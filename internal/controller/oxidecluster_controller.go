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

package controller

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"
	"github.com/oxidecomputer/cluster-api-provider-oxide/internal/cloud"
	"github.com/oxidecomputer/oxide.go/oxide"
)

// OxideClusterReconciler reconciles a OxideCluster object
type OxideClusterReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	OxideClientFactory cloud.OxideClientFactory
}

// TODO: Audit RBAC permissions.
//
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=oxideclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=oxideclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=oxideclusters/finalizers,verbs=update
//
// +kubebuilder:rbac:groups=infrastructure.machine.x-k8s.io,resources=oxidemachines,verbs=get;list;watch
//
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
//
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *OxideClusterReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (_ ctrl.Result, retErr error) {
	log := logf.FromContext(ctx)

	oxideCluster := &infrav1.OxideCluster{}
	if err := r.Get(ctx, req.NamespacedName, oxideCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	patchHelper, err := patch.NewHelper(oxideCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("building patch helper: %w", err)
	}
	defer func() {
		if retErr == nil {
			if err := patchHelper.Patch(ctx, oxideCluster); err != nil {
				retErr = fmt.Errorf("patching cluster: %w", err)
			}
		}
	}()

	cluster, err := util.GetOwnerCluster(ctx, r.Client, oxideCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("missing ownerRef on OxideCluster", "name", oxideCluster.Name)
	}

	oxideClient, err := r.OxideClientFactory(ctx, r.Client, oxideCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile Oxide floating IP, to be attached to an arbitrary control plane instance and used
	// as the control plane endpoint host.
	//
	// TODO: Use a load balancer instead, once Oxide has native load balancing support.
	projectName := oxideCluster.Spec.Project
	ipName := fmt.Sprintf(
		"k8s-cluster-api-endpoint-%s-%s",
		oxideCluster.Namespace,
		oxideCluster.Name,
	)

	if !oxideCluster.DeletionTimestamp.IsZero() {
		if err := r.ensureFloatingIPDeleted(ctx, oxideClient, projectName, ipName); err != nil {
			return ctrl.Result{}, fmt.Errorf("deleting floating ip: %w", err)
		}
		controllerutil.RemoveFinalizer(oxideCluster, infrav1.ClusterFinalizer)
		return ctrl.Result{}, retErr
	}

	controllerutil.AddFinalizer(oxideCluster, infrav1.ClusterFinalizer)

	ip, err := r.ensureFloatingIPExists(ctx, oxideClient, oxideCluster, projectName, ipName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring floating ip: %w", err)
	}
	oxideCluster.Spec.ControlPlaneEndpoint.Host = ip.Ip
	oxideCluster.Spec.ControlPlaneEndpoint.Port = 6443
	oxideCluster.Status.Initialization.Provisioned = new(true)

	// Ensure floating IP is attached to an instance. Use the 0th ready control plane machine if
	// unattached.
	shouldAttach := true
	var machines infrav1.OxideMachineList

	// If the floating IP is attached, check whether it's attached to one of the control plane
	// machines in the
	// current cluster.
	if err := r.List(
		ctx,
		&machines,
		client.InNamespace(oxideCluster.Namespace),
		client.MatchingLabels{
			clusterv1.ClusterNameLabel:         oxideCluster.Name,
			clusterv1.MachineControlPlaneLabel: "",
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing oxide machines: %w", err)
	}
	if ip.InstanceId != "" {
		for _, machine := range machines.Items {
			if machine.Spec.ProviderID != "" {
				instanceID, err := cloud.InstanceIDFromProviderID(machine.Spec.ProviderID)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("parsing provider id: %w", err)
				}
				if instanceID == ip.InstanceId {
					shouldAttach = false
					break
				}
			}
		}
	}

	if shouldAttach {
		log.Info("finding an instance for floating ip attachment", "ip", ip.Ip)

		// If the floating IP is already attached, detach it. This is arguably out of scope for the
		// reconciler, since it's not our responsibility to handle out-of-band changes to the
		// floating IP. But if a deleted cluster failed to clean up its instances or IP, we detach
		// here as a convenience to the user.
		if ip.InstanceId != "" {
			log.Info(
				"floating ip already attached; detaching",
				"ip",
				ip.Ip,
				"instance",
				ip.InstanceId,
			)
			if _, err := oxideClient.FloatingIpDetach(ctx, oxide.FloatingIpDetachParams{
				FloatingIp: oxide.NameOrId(ip.Id),
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("detaching floating ip: %w", err)
			}
		}

		// Attach the floating IP to the 0th provisioned instance.
		for _, machine := range machines.Items {
			if machine.Spec.ProviderID == "" {
				continue
			}
			provisioned := machine.Status.Initialization.Provisioned
			if provisioned == nil || !*provisioned {
				continue
			}
			instanceID, err := cloud.InstanceIDFromProviderID(machine.Spec.ProviderID)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("parsing provider id: %w", err)
			}
			log.Info("attaching floating ip", "ip", ip.Ip, "instance", instanceID)
			if _, err := oxideClient.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
				FloatingIp: oxide.NameOrId(ip.Id),
				Body: &oxide.FloatingIpAttach{
					Kind:   oxide.FloatingIpParentKindInstance,
					Parent: oxide.NameOrId(instanceID),
				},
			}); err != nil {
				return ctrl.Result{}, err
			}
			break
		}

	}

	return ctrl.Result{}, nil
}

// ensureFloatingIPExists creates or views the floating IP.
func (r *OxideClusterReconciler) ensureFloatingIPExists(
	ctx context.Context,
	oxideClient cloud.OxideClient,
	oxideCluster *infrav1.OxideCluster,
	projectName string,
	ipName string,
) (*oxide.FloatingIp, error) {
	log := logf.FromContext(ctx)
	var ip *oxide.FloatingIp
	var err error

	// Create the floating IP, and view it if it already exists.
	ip, err = oxideClient.FloatingIpCreate(ctx, oxide.FloatingIpCreateParams{
		Project: oxide.NameOrId(projectName),
		Body: &oxide.FloatingIpCreate{
			Name: oxide.Name(ipName),
			AddressAllocator: oxide.AddressAllocator{
				Value: oxide.AddressAllocatorAuto{
					PoolSelector: oxide.PoolSelector{
						Value: oxide.PoolSelectorExplicit{
							Pool: oxide.NameOrId(oxideCluster.Spec.IPPool),
						},
					},
				},
			},
		},
	})
	if err != nil {
		if !errors.Is(err, oxide.ErrObjectAlreadyExists) {
			return nil, fmt.Errorf("creating floating ip: %w", err)
		}
		log.Info("floating ip already exists", "name", ipName)
		ip, err = oxideClient.FloatingIpView(ctx, oxide.FloatingIpViewParams{
			Project:    oxide.NameOrId(projectName),
			FloatingIp: oxide.NameOrId(ipName),
		})
		if err != nil {
			return nil, fmt.Errorf("fetching existing floating ip: %w", err)
		}
	}

	return ip, nil
}

// ensureFloatingIPDeleted deletes the floating IP if it exists.
func (r *OxideClusterReconciler) ensureFloatingIPDeleted(
	ctx context.Context,
	oxideClient cloud.OxideClient,
	projectName string,
	ipName string,
) error {
	if err := oxideClient.FloatingIpDelete(ctx, oxide.FloatingIpDeleteParams{
		Project:    oxide.NameOrId(projectName),
		FloatingIp: oxide.NameOrId(ipName),
	}); err != nil {
		if !errors.Is(err, oxide.ErrObjectNotFound) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OxideClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.OxideCluster{}).
		Watches(&infrav1.OxideMachine{}, handler.EnqueueRequestsFromMapFunc(
			r.oxideMachineToOxideCluster,
		)).
		Named("oxidecluster").
		Complete(r)
}

// oxideMachineToOxideCluster watches for machine events and requeues the cluster for update if
// needed. Used to ensure that the floating IP is always attached to an instance.
func (r *OxideClusterReconciler) oxideMachineToOxideCluster(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	clusterName, ok := obj.GetLabels()[clusterv1.ClusterNameLabel]
	if !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      clusterName,
		}},
	}

}
