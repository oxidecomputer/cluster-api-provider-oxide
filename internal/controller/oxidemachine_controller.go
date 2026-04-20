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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
		if retErr == nil {
			if err := patchHelper.Patch(ctx, oxideMachine); err != nil {
				retErr = fmt.Errorf("patching machine: %w", err)
			}
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
	instanceName := fmt.Sprintf(
		"capi-instance-%s-%s-%s",
		oxideMachine.Namespace,
		oxideCluster.Name,
		oxideMachine.Name,
	)
	diskName := fmt.Sprintf(
		"capi-boot-disk-%s-%s-%s",
		oxideMachine.Namespace,
		oxideCluster.Name,
		oxideMachine.Name,
	)

	if !oxideMachine.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, oxideClient, oxideMachine, projectName, instanceName, diskName)
	}

	controllerutil.AddFinalizer(oxideMachine, infrav1.MachineFinalizer)

	// Ensure instance exists. Instance creation idempotently creates the disk and NIC as well, so
	// create all resources in a single request.
	var instance *oxide.Instance
	if oxideMachine.Spec.ProviderID == "" {
		nicName := fmt.Sprintf(
			"capi-nic-%s-%s-%s",
			oxideMachine.Namespace,
			oxideCluster.Name,
			oxideMachine.Name,
		)

		bootstrapSecretName := machine.Spec.Bootstrap.DataSecretName
		if bootstrapSecretName == nil {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
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
				Name:     oxide.Name(instanceName),
				Hostname: oxide.Hostname(instanceName),
				Ncpus:    oxide.InstanceCpuCount(oxideMachine.Spec.NCpus),
				Memory:   oxide.ByteCount(oxideMachine.Spec.Memory.Value()),
				Start:    new(true),
				UserData: base64.StdEncoding.EncodeToString(bootstrapSecret.Data["value"]),
				BootDisk: oxide.InstanceDiskAttachment{
					Value: oxide.InstanceDiskAttachmentCreate{
						Name: oxide.Name(diskName),
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
			if errors.Is(err, oxide.ErrObjectAlreadyExists) {
				instance, err = oxideClient.InstanceView(
					ctx,
					oxide.InstanceViewParams{
						Project:  oxide.NameOrId(projectName),
						Instance: oxide.NameOrId(instanceName),
					},
				)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("viewing existing oxide instance: %w", err)
				}
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

	instanceRunning, err := r.ensureInstanceRunning(ctx, oxideClient, instance)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring instance running: %w", err)
	}
	if !instanceRunning {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	oxideMachine.Status.Initialization.Provisioned = new(true)

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
) (bool, error) {
	log := logf.FromContext(ctx)

	switch instance.RunState {
	case oxide.InstanceStateStopped:
		log.Info("starting instance", "instance", instance.Id)
		if _, err := oxideClient.InstanceStart(ctx, oxide.InstanceStartParams{
			Instance: oxide.NameOrId(instance.Id),
		}); err != nil {
			return false, fmt.Errorf("starting instance: %w", err)
		}
		log.Info("waiting for instance to start", "state", instance.RunState)
		return false, nil
	case oxide.InstanceStateRunning:
		log.Info("instance is running; marking as provisioned", "instance", instance.Name)
		return true, nil
	default:
		log.Info("waiting for instance", "instance", instance.Id, "state", instance.RunState)
		return false, nil
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

	instanceDeleted, err := r.ensureInstanceDeleted(ctx, oxideClient, projectName, instanceName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring instance deleted: %w", err)
	}
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
) (bool, error) {
	log := logf.FromContext(ctx)

	// View the instance. If it doesn't exist, we're done.
	instance, err := oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
		Project:  oxide.NameOrId(projectName),
		Instance: oxide.NameOrId(instanceName),
	})
	if err != nil {
		if errors.Is(err, oxide.ErrObjectNotFound) {
			return true, nil
		}
		return false, fmt.Errorf("viewing instance: %w", err)
	}

	// Instance deletion state machine:
	// * If running, stop and requeue.
	// * If stopped, destroy and requeue.
	// * Else log and requeue.
	switch instance.RunState {
	case oxide.InstanceStateRunning:
		log.Info("stopping instance", "instance", instance.Id)
		if _, err := oxideClient.InstanceStop(ctx, oxide.InstanceStopParams{
			Project:  oxide.NameOrId(projectName),
			Instance: oxide.NameOrId(instanceName),
		}); err != nil {
			return false, fmt.Errorf("stopping instance: %w", err)
		}
		return false, nil
	case oxide.InstanceStateStopped:
		log.Info("destroying instance", "instance", instance.Id)
		if err := oxideClient.InstanceDelete(ctx, oxide.InstanceDeleteParams{
			Project:  oxide.NameOrId(projectName),
			Instance: oxide.NameOrId(instanceName),
		}); err != nil {
			return false, fmt.Errorf("deleting instance: %w", err)
		}
		return false, nil
	default:
		log.Info(
			"waiting for instance; requeueing",
			"instance",
			instance.Id,
			"state",
			instance.RunState,
		)
		return false, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *OxideMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.OxideMachine{}).
		Named("oxidemachine").
		Complete(r)
}
