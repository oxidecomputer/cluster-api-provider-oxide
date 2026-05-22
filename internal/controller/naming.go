package controller

import (
	"fmt"
	"hash/fnv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const maxResourceLength = 63

const (
	instancePrefix   = "capi"
	nicPrefix        = "capi"
	bootDiskPrefix   = "capi-boot"
	floatingIPPrefix = "capi-fip"
	dataDiskPrefix   = "capi-data-%d"
)

func hashTruncateName(name string, maxLength int) string {
	if len(name) > maxLength {
		hasher := fnv.New32a()
		hasher.Write([]byte(name))
		hash := fmt.Sprintf("%x", hasher.Sum32())
		name = fmt.Sprintf("%s-%s", name[:maxLength-len(hash)-1], hash)
	}
	name = strings.ToLower(name)
	name = strings.TrimSuffix(name, "-")
	return name
}

func getResourceName(prefix string, obj metav1.Object) string {
	return hashTruncateName(
		fmt.Sprintf("%s-%s-%s", prefix, obj.GetNamespace(), obj.GetName()),
		maxResourceLength,
	)
}

func getInstanceName(obj metav1.Object) string {
	return getResourceName(instancePrefix, obj)
}

func getBootDiskName(obj metav1.Object) string {
	return getResourceName(bootDiskPrefix, obj)
}

func getNicName(obj metav1.Object) string {
	return getResourceName(nicPrefix, obj)
}

func getFloatingIPName(obj metav1.Object) string {
	return getResourceName(floatingIPPrefix, obj)
}

func getDataDiskName(obj metav1.Object, idx int) string {
	return getResourceName(fmt.Sprintf(dataDiskPrefix, idx), obj)
}
