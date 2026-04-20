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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"
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
				m.EXPECT().FloatingIpCreate(gomock.Any(), gomock.Any()).Return(wantIP, nil)
			},
		},
		{
			name: "adopt",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpCreate(gomock.Any(), gomock.Any()).Return(nil, httpErr("ObjectAlreadyExists"))
				m.EXPECT().FloatingIpView(gomock.Any(), gomock.Any()).Return(wantIP, nil)
			},
		},
		{
			name: "create error",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpCreate(gomock.Any(), gomock.Any()).Return(nil, httpErr("InternalError"))
			},
			wantErr: "creating floating ip",
		},
		{
			name: "view error",
			setup: func(m *mock.MockOxideClient) {
				m.EXPECT().FloatingIpCreate(gomock.Any(), gomock.Any()).Return(nil, httpErr("ObjectAlreadyExists"))
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
