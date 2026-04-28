package e2e

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"
)

const specName = "capox"

var _ = Describe("capox", func() {
	var (
		namespace   *corev1.Namespace
		clusterName string
	)

	BeforeEach(func() {
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", specName, util.RandomString(6)),
			},
		}
		Expect(bootstrapClusterProxy.GetClient().Create(ctx, namespace)).To(Succeed())
		clusterName = fmt.Sprintf("%s-%s", specName, util.RandomString(6))

		// Provision Oxide credentials in the management cluster.
		host := os.Getenv("OXIDE_HOST")
		token := os.Getenv("OXIDE_TOKEN")
		Expect(host).NotTo(BeEmpty(), "OXIDE_HOST must be set")
		Expect(token).NotTo(BeEmpty(), "OXIDE_TOKEN must be set")
		Expect(bootstrapClusterProxy.GetClient().Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "oxide-credentials",
				Namespace: namespace.Name,
			},
			StringData: map[string]string{
				"host":  host,
				"token": token,
			},
		})).To(Succeed())
	})

	AfterEach(func() {
		if skipCleanup || namespace == nil {
			return
		}

		// Delete the cluster and its managed resources. Note: don't delete the namespace until the
		// cluster is fully deleted, else we'll delete the credentials needed to clean up Oxide
		// resources.
		framework.DeleteAllClustersAndWait(ctx, framework.DeleteAllClustersAndWaitInput{
			ClusterProxy:         bootstrapClusterProxy,
			ClusterctlConfigPath: clusterctlConfigPath,
			Namespace:            namespace.Name,
		}, e2eConfig.GetIntervals("default", "wait-delete-cluster")...)

		// Delete the namespace.
		framework.DeleteNamespace(ctx, framework.DeleteNamespaceInput{
			Deleter: bootstrapClusterProxy.GetClient(),
			Name:    namespace.Name,
		})
	})

	It("provisions a minimal cluster", func() {
		clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy: bootstrapClusterProxy,
			ConfigCluster: clusterctl.ConfigClusterInput{
				LogFolder: filepath.Join(
					artifactFolder,
					"clusters",
					bootstrapClusterProxy.GetName(),
				),
				ClusterctlConfigPath:     clusterctlConfigPath,
				KubeconfigPath:           bootstrapClusterProxy.GetKubeconfigPath(),
				InfrastructureProvider:   "oxide",
				Flavor:                   "",
				Namespace:                namespace.Name,
				ClusterName:              clusterName,
				KubernetesVersion:        e2eConfig.MustGetVariable("KUBERNETES_VERSION"),
				ControlPlaneMachineCount: ptr.To[int64](1),
				WorkerMachineCount:       ptr.To[int64](1),
			},
			WaitForClusterIntervals:      e2eConfig.GetIntervals("default", "wait-cluster"),
			WaitForControlPlaneIntervals: e2eConfig.GetIntervals("default", "wait-control-plane"),
			WaitForMachineDeployments:    e2eConfig.GetIntervals("default", "wait-worker-nodes"),
		}, &clusterctl.ApplyClusterTemplateAndWaitResult{})
	})

})
