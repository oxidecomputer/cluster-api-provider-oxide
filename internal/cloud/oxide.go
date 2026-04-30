package cloud

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"

	"github.com/oxidecomputer/oxide.go/oxide"
)

type OxideClientFactory func(context.Context, client.Client, *infrav1.OxideCluster) (OxideClient, error)

//go:generate go tool -modfile=../../tools/go.mod mockgen -source=oxide.go -destination=mock/mock_client.go -package=mock
type OxideClient interface {
	FloatingIpCreate(context.Context, oxide.FloatingIpCreateParams) (*oxide.FloatingIp, error)
	FloatingIpView(context.Context, oxide.FloatingIpViewParams) (*oxide.FloatingIp, error)
	FloatingIpAttach(context.Context, oxide.FloatingIpAttachParams) (*oxide.FloatingIp, error)
	FloatingIpDetach(context.Context, oxide.FloatingIpDetachParams) (*oxide.FloatingIp, error)
	FloatingIpDelete(context.Context, oxide.FloatingIpDeleteParams) error

	InstanceCreate(context.Context, oxide.InstanceCreateParams) (*oxide.Instance, error)
	InstanceView(context.Context, oxide.InstanceViewParams) (*oxide.Instance, error)
	InstanceStart(context.Context, oxide.InstanceStartParams) (*oxide.Instance, error)
	InstanceStop(context.Context, oxide.InstanceStopParams) (*oxide.Instance, error)
	InstanceDelete(context.Context, oxide.InstanceDeleteParams) error
	InstanceNetworkInterfaceListAllPages(
		context.Context,
		oxide.InstanceNetworkInterfaceListParams,
	) ([]oxide.InstanceNetworkInterface, error)

	DiskDelete(context.Context, oxide.DiskDeleteParams) error
}

const (
	SecretDataHostKey  = "host"
	SecretDataTokenKey = "token"
)

// NewOxideClient constructs an oxide.Client using the secret reference from the provided
// OxideCluster.
func NewOxideClient(
	ctx context.Context,
	k8sClient client.Client,
	oxideCluster *infrav1.OxideCluster,
) (OxideClient, error) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: oxideCluster.Spec.CredentialsRef.Namespace,
		Name:      oxideCluster.Spec.CredentialsRef.Name,
	}, secret); err != nil {
		return nil, fmt.Errorf("loading oxide credentials: %w", err)
	}
	oxideClient, err := oxide.NewClient(
		oxide.WithHost(string(secret.Data[SecretDataHostKey])),
		oxide.WithToken(string(secret.Data[SecretDataTokenKey])),
	)
	if err != nil {
		return nil, fmt.Errorf("constructing oxide client: %w", err)
	}
	return oxideClient, nil
}
