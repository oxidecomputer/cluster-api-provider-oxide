package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashTruncateName(t *testing.T) {
	for _, tc := range []struct {
		name         string
		resourceName string
		maxLength    int
		want         string
	}{
		{
			name:         "short",
			resourceName: "short",
			maxLength:    64,
			want:         "short",
		},
		{
			name:         "long",
			resourceName: "longlonglonglonglong",
			maxLength:    16,
			want:         "longlon-7f480963",
		},
		{
			name:         "trailing-dash",
			resourceName: "abc-",
			maxLength:    64,
			want:         "abc",
		},
		{
			name:         "uppercase",
			resourceName: "ABC",
			maxLength:    64,
			want:         "abc",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hashTruncateName(tc.resourceName, tc.maxLength)
			assert.Equal(t, tc.want, got)
		})
	}
}
