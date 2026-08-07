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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"
	"github.com/oxidecomputer/cluster-api-provider-oxide/internal/cloud"
	"github.com/oxidecomputer/cluster-api-provider-oxide/internal/cloud/mock"
	"github.com/oxidecomputer/oxide.go/oxide"
)

// httpErr constructs an *oxide.HTTPError with a stub HTTPResponse so that its
// Error() method can be called without nil-dereferencing.
func httpErr(code string) *oxide.HTTPError {
	return &oxide.HTTPError{
		ErrorResponse: &oxide.ErrorResponse{ErrorCode: code},
		HTTPResponse:  &http.Response{Request: &http.Request{}},
	}
}

// newPauseTestScheme builds a scheme with the CAPI and Oxide types registered.
func newPauseTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	assert.NoError(t, clusterv1.AddToScheme(scheme))
	assert.NoError(t, infrav1.AddToScheme(scheme))
	return scheme
}

// getPausedCondition re-fetches obj and returns its Paused condition, or nil if unset.
func getPausedCondition(t *testing.T, c client.Client, obj interface {
	client.Object
	conditions.Getter
}) *metav1.Condition {
	t.Helper()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatalf("getting %T: %v", obj, err)
	}
	return conditions.Get(obj, clusterv1.PausedCondition)
}

func TestOxideClusterReconcilePaused(t *testing.T) {
	scheme := newPauseTestScheme(t)
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       clusterv1.ClusterSpec{Paused: new(true)},
	}
	oxideCluster := &infrav1.OxideCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(),
				Kind:       "Cluster",
				Name:       "test",
				UID:        "test-uid",
			}},
		},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, oxideCluster).
		WithStatusSubresource(&infrav1.OxideCluster{}).
		Build()

	factoryCalls := 0
	r := &OxideClusterReconciler{
		Client: k8sClient,
		Scheme: scheme,
		OxideClientFactory: func(context.Context, client.Client, *infrav1.OxideCluster) (cloud.OxideClient, error) {
			factoryCalls++
			return nil, errors.New("halting test reconcile")
		},
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"}}

	// While paused, the first reconcile sets the Paused condition and requeues, and subsequent
	// reconciles skip. The Oxide client must never be constructed.
	for range 2 {
		result, err := r.Reconcile(ctx, req)
		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	}
	assert.Equal(t, 0, factoryCalls)
	if cond := getPausedCondition(t, k8sClient, oxideCluster); assert.NotNil(t, cond) {
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
	}

	// Unpause the Cluster. The next reconcile only flips the Paused condition; the one after
	// resumes normal reconciliation and constructs the Oxide client.
	assert.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster))
	cluster.Spec.Paused = new(false)
	assert.NoError(t, k8sClient.Update(ctx, cluster))

	_, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, 0, factoryCalls)
	if cond := getPausedCondition(t, k8sClient, oxideCluster); assert.NotNil(t, cond) {
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
	}

	_, err = r.Reconcile(ctx, req)
	assert.ErrorContains(t, err, "halting test reconcile")
	assert.Equal(t, 1, factoryCalls)
}

func TestEnsureFloatingIPExists(t *testing.T) {
	wantIP := &oxide.FloatingIp{
		Id: "ip-id",
		Ip: "1.2.3.4",
	}

	for _, tc := range []struct {
		name    string
		setup   func(*mock.MockOxideClient)
		wantErr string
	}{
		{
			name: "create",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpView(gomock.Any(), gomock.Any()).Return(nil, httpErr("ObjectNotFound"))
				m.EXPECT().FloatingIpCreate(gomock.Any(), gomock.Any()).Return(wantIP, nil)
			},
		},
		{
			name: "adopt",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpView(gomock.Any(), gomock.Any()).Return(wantIP, nil)
			},
		},
		{
			name: "create error",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpView(gomock.Any(), gomock.Any()).Return(nil, httpErr("ObjectNotFound"))
				m.EXPECT().FloatingIpCreate(gomock.Any(), gomock.Any()).Return(nil, httpErr("InternalError"))
			},
			wantErr: "creating floating ip",
		},
		{
			name: "view error",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpView(gomock.Any(), gomock.Any()).Return(nil, httpErr("InternalError"))
			},
			wantErr: "fetching existing floating ip",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			oxideClient := mock.NewMockOxideClient(ctrl)
			tc.setup(oxideClient)

			cluster := &infrav1.OxideCluster{
				Spec: infrav1.OxideClusterSpec{IPPool: "default"},
			}
			r := OxideClusterReconciler{}
			gotIP, gotErr := r.ensureFloatingIPExists(
				context.Background(),
				oxideClient,
				cluster,
				"project",
				"ip-name",
			)
			if tc.wantErr != "" {
				assert.ErrorContains(t, gotErr, tc.wantErr)
				assert.Nil(t, gotIP)
			} else {
				assert.NoError(t, gotErr)
				assert.Equal(t, wantIP, gotIP)
			}
		})
	}
}

func TestEnsureFloatingIPDeleted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(*mock.MockOxideClient)
		wantErr string
	}{
		{
			name: "delete",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpDelete(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			name: "gone",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpDelete(gomock.Any(), gomock.Any()).Return(httpErr("ObjectNotFound"))
			},
		},
		{
			name: "delete error",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpDelete(gomock.Any(), gomock.Any()).Return(httpErr("InternalError"))
			},
			wantErr: "InternalError",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			oxideClient := mock.NewMockOxideClient(ctrl)
			tc.setup(oxideClient)

			r := OxideClusterReconciler{}
			gotErr := r.ensureFloatingIPDeleted(
				context.Background(),
				oxideClient,
				"project",
				"ip-name",
			)
			if tc.wantErr != "" {
				assert.ErrorContains(t, gotErr, tc.wantErr)
			} else {
				assert.NoError(t, gotErr)
			}
		})
	}
}

func TestFloatingIPAllocator(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cluster *infrav1.OxideCluster
		want    oxide.AddressAllocator
	}{
		{
			name: "endpoint specified",
			cluster: &infrav1.OxideCluster{
				Spec: infrav1.OxideClusterSpec{
					ControlPlaneEndpoint: clusterv1.APIEndpoint{
						Host: "1.2.3.4",
					},
				},
			},
			want: oxide.AddressAllocator{
				Value: oxide.AddressAllocatorExplicit{
					Ip: "1.2.3.4",
				},
			},
		},
		{
			name: "pool specified",
			cluster: &infrav1.OxideCluster{
				Spec: infrav1.OxideClusterSpec{
					IPPool: "not-default",
				},
			},
			want: oxide.AddressAllocator{
				Value: oxide.AddressAllocatorAuto{
					PoolSelector: oxide.PoolSelector{
						Value: oxide.PoolSelectorExplicit{
							Pool: oxide.NameOrId("not-default"),
						},
					},
				},
			},
		},
		{
			name: "type specified",
			cluster: &infrav1.OxideCluster{
				Spec: infrav1.OxideClusterSpec{
					IPType: "v6",
				},
			},
			want: oxide.AddressAllocator{
				Value: oxide.AddressAllocatorAuto{
					PoolSelector: oxide.PoolSelector{
						Value: oxide.PoolSelectorAuto{
							IpVersion: oxide.IpVersion("v6"),
						},
					},
				},
			},
		},
		{
			name:    "default",
			cluster: &infrav1.OxideCluster{},
			want: oxide.AddressAllocator{
				Value: oxide.AddressAllocatorAuto{
					PoolSelector: oxide.PoolSelector{
						Value: oxide.PoolSelectorAuto{
							IpVersion: oxide.IpVersion(""),
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := floatingIPAllocator(tc.cluster)
			assert.Equal(t, tc.want, got)
		})
	}
}
