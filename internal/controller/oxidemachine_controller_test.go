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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"
	"github.com/oxidecomputer/cluster-api-provider-oxide/internal/cloud/mock"
	"github.com/oxidecomputer/oxide.go/oxide"
)

func TestEnsureInstanceRunning(t *testing.T) {
	for _, tc := range []struct {
		name        string
		instance    *oxide.Instance
		setup       func(*mock.MockOxideClient)
		wantRunning bool
		wantErr     string
	}{
		{
			name:     "stopped",
			instance: &oxide.Instance{RunState: oxide.InstanceStateStopped},
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceStart(gomock.Any(), gomock.Any()).Return(&oxide.Instance{}, nil)
			},
		},
		{
			name:     "starting",
			instance: &oxide.Instance{RunState: oxide.InstanceStateStarting},
			setup:    func(m *mock.MockOxideClient) {},
		},
		{
			name:        "running",
			instance:    &oxide.Instance{RunState: oxide.InstanceStateRunning},
			setup:       func(m *mock.MockOxideClient) {},
			wantRunning: true,
		},
		{
			name:     "start error",
			instance: &oxide.Instance{RunState: oxide.InstanceStateStopped},
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceStart(gomock.Any(), gomock.Any()).Return(nil, httpErr("InternalError"))
			},
			wantErr: "starting instance",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			oxideClient := mock.NewMockOxideClient(ctrl)
			tc.setup(oxideClient)

			r := OxideMachineReconciler{}
			gotRunning, _, gotErr := r.ensureInstanceRunning(
				context.Background(),
				oxideClient,
				tc.instance,
			)
			assert.Equal(t, tc.wantRunning, gotRunning)
			if tc.wantErr != "" {
				assert.ErrorContains(t, gotErr, tc.wantErr)
			} else {
				assert.NoError(t, gotErr)
			}
		})
	}
}

func TestEnsureInstanceDeleted(t *testing.T) {
	for _, tc := range []struct {
		name        string
		setup       func(*mock.MockOxideClient)
		wantDeleted bool
		wantErr     string
	}{
		{
			name: "gone",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceView(gomock.Any(), gomock.Any()).Return(nil, httpErr("ObjectNotFound"))
			},
			wantDeleted: true,
		},
		{
			name: "running",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceView(gomock.Any(), gomock.Any()).
					Return(&oxide.Instance{RunState: oxide.InstanceStateRunning}, nil)
				m.EXPECT().InstanceStop(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name: "stopped",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceView(gomock.Any(), gomock.Any()).
					Return(&oxide.Instance{RunState: oxide.InstanceStateStopped}, nil)
				m.EXPECT().InstanceDelete(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			name: "stopping",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceView(gomock.Any(), gomock.Any()).
					Return(&oxide.Instance{RunState: oxide.InstanceStateStopping}, nil)
			},
		},
		{
			name: "view error",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().InstanceView(gomock.Any(), gomock.Any()).Return(nil, httpErr("InternalError"))
			},
			wantErr: "viewing instance",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			oxideClient := mock.NewMockOxideClient(ctrl)
			tc.setup(oxideClient)

			r := OxideMachineReconciler{}
			gotDeleted, _, gotErr := r.ensureInstanceDeleted(
				context.Background(),
				oxideClient,
				"project",
				"instance",
			)
			assert.Equal(t, tc.wantDeleted, gotDeleted)
			if tc.wantErr != "" {
				assert.ErrorContains(t, gotErr, tc.wantErr)
			} else {
				assert.NoError(t, gotErr)
			}
		})
	}
}

func TestMachineAddressesFromNICs(t *testing.T) {
	for _, tc := range []struct {
		name          string
		nics          []oxide.InstanceNetworkInterface
		wantAddresses []clusterv1.MachineAddress
	}{
		{
			name: "v4",
			nics: []oxide.InstanceNetworkInterface{
				{
					IpStack: oxide.PrivateIpStack{
						Value: &oxide.PrivateIpStackV4{
							Value: oxide.PrivateIpv4Stack{
								Ip: "192.0.0.1",
							},
						},
					},
				},
			},
			wantAddresses: []clusterv1.MachineAddress{
				{Type: clusterv1.MachineInternalIP, Address: "192.0.0.1"},
			},
		},
		{
			name: "v6",
			nics: []oxide.InstanceNetworkInterface{
				{
					IpStack: oxide.PrivateIpStack{
						Value: &oxide.PrivateIpStackV6{
							Value: oxide.PrivateIpv6Stack{
								Ip: "2001:db8::1",
							},
						},
					},
				},
			},
			wantAddresses: []clusterv1.MachineAddress{
				{Type: clusterv1.MachineInternalIP, Address: "2001:db8::1"},
			},
		},
		{
			name: "dualstack",
			nics: []oxide.InstanceNetworkInterface{
				{
					IpStack: oxide.PrivateIpStack{
						Value: &oxide.PrivateIpStackDualStack{
							Value: oxide.PrivateIpStackDualStackValue{
								V4: oxide.PrivateIpv4Stack{
									Ip: "192.0.0.2",
								},
								V6: oxide.PrivateIpv6Stack{
									Ip: "2001:db8::2",
								},
							},
						},
					},
				},
			},
			wantAddresses: []clusterv1.MachineAddress{
				{Type: clusterv1.MachineInternalIP, Address: "192.0.0.2"},
				{Type: clusterv1.MachineInternalIP, Address: "2001:db8::2"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addresses, err := machineAddressesFromNICs(tc.nics)
			assert.NoError(t, err)
			assert.ElementsMatch(t, addresses, tc.wantAddresses)
		})
	}
}

func TestExternalIPsFromMachine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		machine *infrav1.OxideMachine
		want    []oxide.ExternalIpCreate
	}{
		{
			name: "pool",
			machine: &infrav1.OxideMachine{
				Spec: infrav1.OxideMachineSpec{
					IPPool: "pool",
				},
			},
			want: []oxide.ExternalIpCreate{
				{
					Value: oxide.ExternalIpCreateEphemeral{
						PoolSelector: oxide.PoolSelector{
							Value: oxide.PoolSelectorExplicit{
								Pool: oxide.NameOrId("pool"),
							},
						},
					},
				},
			},
		},
		{
			name: "no pool",
			machine: &infrav1.OxideMachine{
				Spec: infrav1.OxideMachineSpec{},
			},
			want: []oxide.ExternalIpCreate{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := externalIPsFromMachine(tc.machine)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestDisksFromOxideMachine(t *testing.T) {
	machine := &infrav1.OxideMachine{
		Spec: infrav1.OxideMachineSpec{
			DataDisks: []infrav1.DataDisk{
				{
					Size: resource.MustParse("20Gi"),
				},
				{
					Size:      resource.MustParse("10Gi"),
					BlockSize: 512,
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine",
			Namespace: "default",
		},
	}
	got := disksFromOxideMachine(machine)
	want := []oxide.InstanceDiskAttachment{
		{
			Value: oxide.InstanceDiskAttachmentCreate{
				Name: "capi-data-0-default-machine",
				Size: oxide.ByteCount(20 * 1024 * 1024 * 1024),
				DiskBackend: oxide.DiskBackend{
					Value: oxide.DiskBackendDistributed{
						DiskSource: oxide.DiskSource{
							Value: oxide.DiskSourceBlank{
								BlockSize: 0,
							},
						},
					},
				},
			},
		},
		{
			Value: oxide.InstanceDiskAttachmentCreate{
				Name: "capi-data-1-default-machine",
				Size: oxide.ByteCount(10 * 1024 * 1024 * 1024),
				DiskBackend: oxide.DiskBackend{
					Value: oxide.DiskBackendDistributed{
						DiskSource: oxide.DiskSource{
							Value: oxide.DiskSourceBlank{
								BlockSize: 512,
							},
						},
					},
				},
			},
		},
	}
	assert.Equal(t, got, want)
}
