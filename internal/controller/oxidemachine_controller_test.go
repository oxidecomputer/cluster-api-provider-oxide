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

	"github.com/oxidecomputer/cluster-api-provider-oxide/internal/cloud/mock"
	"github.com/oxidecomputer/oxide.go/oxide"
)

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
			gotDeleted, gotErr := r.ensureInstanceDeleted(
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
				m.EXPECT().InstanceStart(gomock.Any(), gomock.Any()).Return(nil, nil)
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
			gotRunning, gotErr := r.ensureInstanceRunning(
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
