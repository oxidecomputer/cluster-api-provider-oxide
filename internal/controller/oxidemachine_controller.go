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
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"
	"github.com/oxidecomputer/cluster-api-provider-oxide/internal/cloud"
	"github.com/oxidecomputer/oxide.go/oxide"
)

// OxideMachineReconciler reconciles a OxideMachine object
type OxideMachineReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	OxideClientFactory cloud.OxideClientFactory
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=oxidemachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=oxidemachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=oxidemachines/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the OxideMachine object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *OxideMachineReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (_ ctrl.Result, retErr error) {
	log := logf.FromContext(ctx)

	oxideMachine := &infrav1.OxideMachine{}
	if err := r.Get(ctx, req.NamespacedName, oxideMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	patchHelper, err := patch.NewHelper(oxideMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("building patch helper: %w", err)
	}
	defer func() {
		if err := patchHelper.Patch(ctx, oxideMachine); err != nil {
			retErr = fmt.Errorf("patching machine: %w", err)
		}
	}()

	machine, err := util.GetOwnerMachine(ctx, r.Client, oxideMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		log.Info("waiting for owner machine reference")
		return ctrl.Result{}, nil
	}

	clusterName := machine.Labels[clusterv1.ClusterNameLabel]

	oxideCluster := &infrav1.OxideCluster{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: machine.Namespace,
		Name:      clusterName,
	}, oxideCluster); err != nil {
		return ctrl.Result{}, err
	}

	oxideClient, err := r.OxideClientFactory(ctx, r.Client, oxideCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("constructing oxide client: %w", err)
	}

	projectName := oxideCluster.Spec.Project
	instanceName := getInstanceName(oxideMachine)
	bootDiskName := getBootDiskName(oxideMachine)
	nicName := getNicName(oxideMachine)

	if !oxideMachine.DeletionTimestamp.IsZero() {
		return r.handleDelete(
			ctx,
			oxideClient,
			oxideMachine,
			projectName,
			instanceName,
			bootDiskName,
		)
	}

	controllerutil.AddFinalizer(oxideMachine, infrav1.MachineFinalizer)

	// Ensure instance exists. Instance creation idempotently creates the disk and NIC as well, so
	// create all resources in a single request.
	var instance *oxide.Instance
	if oxideMachine.Spec.ProviderID == "" {
		// Fetch the UserData from the bootstrap secret. If the secret isn't set on the spec yet,
		// mark the OxideMachine as unready, and wait for an update to DataSecretName to trigger a
		// new reconcile.
		bootstrapSecretName := machine.Spec.Bootstrap.DataSecretName
		if bootstrapSecretName == nil {
			conditions.Set(oxideMachine, metav1.Condition{
				Type:   clusterv1.ReadyCondition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1.WaitingForBootstrapDataReason,
			})
			return ctrl.Result{}, nil
		}
		var bootstrapSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: machine.Namespace,
			Name:      *bootstrapSecretName,
		}, &bootstrapSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("fetching bootstrap secret: %w", err)
		}
		if _, ok := bootstrapSecret.Data["value"]; !ok {
			return ctrl.Result{}, fmt.Errorf(
				"missing `value` key in bootstrap secret %s",
				*bootstrapSecretName,
			)
		}

		instance, err = oxideClient.InstanceCreate(ctx, oxide.InstanceCreateParams{
			Project: oxide.NameOrId(projectName),
			Body: &oxide.InstanceCreate{
				Name:               oxide.Name(instanceName),
				Hostname:           oxide.Hostname(instanceName),
				Ncpus:              oxide.InstanceCpuCount(oxideMachine.Spec.NCpus),
				Memory:             oxide.ByteCount(oxideMachine.Spec.Memory.Value()),
				Start:              new(true),
				AntiAffinityGroups: toNamesOrIds(oxideMachine.Spec.AntiAffinityGroups),
				SshPublicKeys:      toNamesOrIds(oxideMachine.Spec.SSHPublicKeys),
				UserData: base64.StdEncoding.EncodeToString(
					bootstrapSecret.Data["value"],
				),
				BootDisk: oxide.InstanceDiskAttachment{
					Value: oxide.InstanceDiskAttachmentCreate{
						Name: oxide.Name(bootDiskName),
						Size: oxide.ByteCount(oxideMachine.Spec.DiskSize.Value()),
						DiskBackend: oxide.DiskBackend{
							Value: oxide.DiskBackendDistributed{
								DiskSource: oxide.DiskSource{
									Value: oxide.DiskSourceImage{
										ImageId: oxideMachine.Spec.ImageID,
									},
								},
							},
						},
					},
				},
				NetworkInterfaces: oxide.InstanceNetworkInterfaceAttachment{
					Value: oxide.InstanceNetworkInterfaceAttachmentCreate{
						Params: []oxide.InstanceNetworkInterfaceCreate{
							{
								Name:       oxide.Name(nicName),
								VpcName:    oxide.Name(oxideCluster.Spec.VPC),
								SubnetName: oxide.Name(oxideCluster.Spec.Subnet),
							},
						},
					},
				},
			},
		})
		if err != nil {
			// Look up the instance if creation failed with a conflict, and adopt the existing
			// instance if found. Note: if an instance was created out of band with unexpected
			// parameters, it will be adopted as well; operators shouldn't create or modify these
			// instances outside the reconciler.
			if !errors.Is(err, oxide.ErrObjectAlreadyExists) {
				return ctrl.Result{}, fmt.Errorf("creating oxide instance: %w", err)
			}
			instance, err = oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
				Project:  oxide.NameOrId(projectName),
				Instance: oxide.NameOrId(instanceName),
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("viewing existing oxide instance: %w", err)
			}
		}

		oxideMachine.Spec.ProviderID = cloud.NewProviderID(instance.Id)
	} else {
		instance, err = oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
			Project:  oxide.NameOrId(projectName),
			Instance: oxide.NameOrId(instanceName),
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("fetching oxide instance: %w", err)
		}
	}

	instanceRunning, instance, err := r.ensureInstanceRunning(ctx, oxideClient, instance)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring instance running: %w", err)
	}
	if !instanceRunning {
		conditions.Set(oxideMachine, metav1.Condition{
			Type:   clusterv1.ReadyCondition,
			Status: metav1.ConditionFalse,
			Reason: getReadyReason(instance),
		})
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	conditions.Set(oxideMachine, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: getReadyReason(instance),
	})
	oxideMachine.Status.Initialization.Provisioned = new(true)

	// Look up instance addresses if not already known. As of this writing, the controller isn't
	// responsible for managing NIC attachments after instance creation, so we don't need to refresh
	// after populating the address list initially.
	if len(oxideMachine.Status.Addresses) == 0 {
		nics, err := oxideClient.InstanceNetworkInterfaceListAllPages(
			ctx,
			oxide.InstanceNetworkInterfaceListParams{
				Instance: oxide.NameOrId(instance.Id),
			},
		)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("listing instance NICs: %w", err)
		}
		addresses, err := machineAddressesFromNICs(nics)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting NIC addresses: %w", err)
		}
		oxideMachine.Status.Addresses = addresses
	}

	return ctrl.Result{}, nil
}

// ensureInstanceRunning ensures that the given instance is running, starting it if necessary.
//
// * If stopped, start and requeue.
// * If running, finished.
// * Else log and requeue.
func (r *OxideMachineReconciler) ensureInstanceRunning(
	ctx context.Context,
	oxideClient cloud.OxideClient,
	instance *oxide.Instance,
) (bool, *oxide.Instance, error) {
	log := logf.FromContext(ctx)

	switch instance.RunState {
	case oxide.InstanceStateStopped:
		log.Info("starting instance", "instance", instance.Id)
		startedInstance, err := oxideClient.InstanceStart(ctx, oxide.InstanceStartParams{
			Instance: oxide.NameOrId(instance.Id),
		})
		if err != nil {
			return false, instance, fmt.Errorf("starting instance: %w", err)
		}
		instance = startedInstance
		log.Info("waiting for instance to start", "state", instance.RunState)
		return false, instance, nil
	case oxide.InstanceStateRunning:
		log.Info("instance is running; marking as provisioned", "instance", instance.Name)
		return true, instance, nil
	default:
		log.Info("waiting for instance", "instance", instance.Id, "state", instance.RunState)
		return false, instance, nil
	}
}

// handleDelete idempotently deletes the Oxide instance and its boot disk, and removes the finalizer
// if successful.
func (r *OxideMachineReconciler) handleDelete(
	ctx context.Context,
	oxideClient cloud.OxideClient,
	oxideMachine *infrav1.OxideMachine,
	projectName string,
	instanceName string,
	diskName string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	instanceDeleted, instance, err := r.ensureInstanceDeleted(
		ctx,
		oxideClient,
		projectName,
		instanceName,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring instance deleted: %w", err)
	}
	conditions.Set(oxideMachine, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionFalse,
		Reason: getReadyReason(instance),
	})
	if !instanceDeleted {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Ensure the boot disk is deleted. Because we just ensured that the instance is destroyed, we
	// assume the disk isn't attached. If the disk was attached to another instance out of band, or
	// is otherwise in an unexpected state, the reconciler isn't responsible for detaching it, and
	// returns an error.
	log.Info("deleting disk", "disk", diskName)
	if err := oxideClient.DiskDelete(ctx, oxide.DiskDeleteParams{
		Project: oxide.NameOrId(projectName),
		Disk:    oxide.NameOrId(diskName),
	}); err != nil {
		if !errors.Is(err, oxide.ErrObjectNotFound) {
			return ctrl.Result{}, fmt.Errorf("deleting disk: %w", err)
		}
	}

	controllerutil.RemoveFinalizer(oxideMachine, infrav1.MachineFinalizer)
	return ctrl.Result{}, nil
}

func (r *OxideMachineReconciler) ensureInstanceDeleted(
	ctx context.Context,
	oxideClient cloud.OxideClient,
	projectName string,
	instanceName string,
) (bool, *oxide.Instance, error) {
	log := logf.FromContext(ctx)

	// View the instance. If it doesn't exist, we're done.
	instance, err := oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
		Project:  oxide.NameOrId(projectName),
		Instance: oxide.NameOrId(instanceName),
	})
	if err != nil {
		if errors.Is(err, oxide.ErrObjectNotFound) {
			return true, nil, nil
		}
		return false, nil, fmt.Errorf("viewing instance: %w", err)
	}

	// Instance deletion state machine:
	// * If running, stop and requeue.
	// * If stopped, destroy and requeue.
	// * Else log and requeue.
	switch instance.RunState {
	case oxide.InstanceStateRunning:
		log.Info("stopping instance", "instance", instance.Id)
		instance, err = oxideClient.InstanceStop(ctx, oxide.InstanceStopParams{
			Project:  oxide.NameOrId(projectName),
			Instance: oxide.NameOrId(instanceName),
		})
		if err != nil {
			return false, instance, fmt.Errorf("stopping instance: %w", err)
		}
		return false, instance, nil
	case oxide.InstanceStateStopped:
		log.Info("destroying instance", "instance", instance.Id)
		if err := oxideClient.InstanceDelete(ctx, oxide.InstanceDeleteParams{
			Project:  oxide.NameOrId(projectName),
			Instance: oxide.NameOrId(instanceName),
		}); err != nil {
			return false, instance, fmt.Errorf("deleting instance: %w", err)
		}
		return false, nil, nil
	default:
		log.Info(
			"waiting for instance; requeueing",
			"instance",
			instance.Id,
			"state",
			instance.RunState,
		)
		return false, instance, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *OxideMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.OxideMachine{}).
		// The OxideMachine reconciler depends on the state of the parent Machine; watch the parent
		// for changes.
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.MachineToInfrastructureMapFunc(infrav1.GroupVersion.WithKind("OxideMachine"))),
		).
		Named("oxidemachine").
		Complete(r)
}

func toNamesOrIds(values []string) []oxide.NameOrId {
	namesOrIds := make([]oxide.NameOrId, 0, len(values))
	for _, value := range values {
		namesOrIds = append(namesOrIds, oxide.NameOrId(value))
	}
	return namesOrIds
}

// getReadyReason builds a Reason for the Ready condition.
func getReadyReason(instance *oxide.Instance) string {
	if instance == nil {
		return infrav1.ReasonInstanceDeleted
	}
	s := string(instance.RunState)
	if s == "" {
		return infrav1.ReasonInstanceUnknown
	}
	return "Instance" + strings.ToUpper(s[:1]) + s[1:]
}

func machineAddressesFromNICs(
	nics []oxide.InstanceNetworkInterface,
) ([]clusterv1.MachineAddress, error) {
	var addresses []clusterv1.MachineAddress
	for _, nic := range nics {
		if v4, ok := nic.IpStack.AsV4(); ok {
			addresses = append(addresses, clusterv1.MachineAddress{
				Type: clusterv1.MachineInternalIP, Address: v4.Value.Ip,
			})
			continue
		}
		if v6, ok := nic.IpStack.AsV6(); ok {
			addresses = append(addresses, clusterv1.MachineAddress{
				Type: clusterv1.MachineInternalIP, Address: v6.Value.Ip,
			})
			continue
		}
		if ds, ok := nic.IpStack.AsDualStack(); ok {
			addresses = append(
				addresses,
				clusterv1.MachineAddress{
					Type:    clusterv1.MachineInternalIP,
					Address: ds.Value.V4.Ip,
				},
				clusterv1.MachineAddress{
					Type:    clusterv1.MachineInternalIP,
					Address: ds.Value.V6.Ip,
				},
			)
			continue
		}
		return nil, fmt.Errorf("unexpected IpStack type %T for NIC %s", nic.IpStack.Value, nic.Id)
	}
	return addresses, nil
}
