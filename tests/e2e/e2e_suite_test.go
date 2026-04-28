package e2e

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	ctrl "sigs.k8s.io/controller-runtime"

	infrav1 "github.com/oxidecomputer/cluster-api-provider-oxide/api/v1alpha1"
)

// Test suite flags.
var (
	// configPath is the path to the e2e config file.
	configPath string

	// artifactFolder is the folder to store e2e test artifacts.
	artifactFolder string

	// skipCleanup prevents cleanup of test resources e.g. for debug purposes.
	skipCleanup bool
)

// Test suite global vars.
var (
	ctx = ctrl.SetupSignalHandler()

	// e2eConfig to be used for this test, read from configPath.
	e2eConfig *clusterctl.E2EConfig

	// clusterctlConfigPath to be used for this test, created by generating a clusterctl local
	// repository
	// with the providers specified in the configPath.
	clusterctlConfigPath string

	// bootstrapClusterProxy allows to interact with the bootstrap cluster to be used for the e2e
	// tests.
	bootstrapClusterProxy framework.ClusterProxy
)

func init() {
	flag.StringVar(&configPath, "e2e.config", "", "path to the e2e config file")
	flag.StringVar(
		&artifactFolder,
		"e2e.artifacts-folder",
		"",
		"folder where e2e test artifact should be stored",
	)
	flag.BoolVar(
		&skipCleanup,
		"e2e.skip-resource-cleanup",
		false,
		"if true, the resource cleanup after tests will be skipped",
	)

	ctrl.SetLogger(klog.Background())
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "capox-e2e")
}

var _ = BeforeSuite(func() {
	e2eConfig = clusterctl.LoadE2EConfig(ctx, clusterctl.LoadE2EConfigInput{ConfigPath: configPath})
	Expect(e2eConfig).ToNot(BeNil(), "error loading e2e config from %s", configPath)

	clusterctlConfigPath = createClusterctlLocalRepository(
		e2eConfig,
		filepath.Join(artifactFolder, "repository"),
	)

	scheme := initScheme()

	kubeconfigPath := os.Getenv("KUBECONFIG")
	Expect(kubeconfigPath).NotTo(BeEmpty(), "empty KUBECONFIG")
	bootstrapClusterProxy = framework.NewClusterProxy("bootstrap", kubeconfigPath, scheme)
	Expect(bootstrapClusterProxy).NotTo(BeNil(), "error building cluster proxy")

	clusterctl.InitManagementClusterAndWatchControllerLogs(
		ctx,
		clusterctl.InitManagementClusterAndWatchControllerLogsInput{
			ClusterProxy:            bootstrapClusterProxy,
			ClusterctlConfigPath:    clusterctlConfigPath,
			InfrastructureProviders: e2eConfig.InfrastructureProviders(),
			LogFolder: filepath.Join(
				artifactFolder,
				"clusters",
				bootstrapClusterProxy.GetName(),
			),
		},
		e2eConfig.GetIntervals(bootstrapClusterProxy.GetName(), "wait-controllers")...)
})

var _ = AfterSuite(func() {
	if bootstrapClusterProxy != nil {
		bootstrapClusterProxy.Dispose(ctx)
	}
})

func initScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	framework.TryAddDefaultSchemes(scheme)
	_ = infrav1.AddToScheme(scheme)
	return scheme
}

func createClusterctlLocalRepository(config *clusterctl.E2EConfig, repositoryFolder string) string {
	createRepositoryInput := clusterctl.CreateRepositoryInput{
		E2EConfig:        config,
		RepositoryFolder: repositoryFolder,
	}

	// Install a CNI plugin into the workload cluster. Look up the CNI manifest configured in the
	// test config, render it to a ConfigMap, and inject the ConfigMap data via the CNI_RESOURCES
	// environment variable. The ClusterResourceSet will apply the ConfigMap data into the workload
	// cluster.
	cniPath := config.MustGetVariable("CNI")
	Expect(cniPath).To(BeAnExistingFile(), "CNI path %q does not exist", cniPath)
	createRepositoryInput.RegisterClusterResourceSetConfigMapTransformation(
		cniPath,
		"CNI_RESOURCES",
	)

	// Configure the Oxide cloud controller manager via ClusterResourceSet, as above. The rendered
	// Helm chart doesn't include the Oxide secret, so we include it in the ClusterResourceSet
	// separately.
	ccmPath := config.MustGetVariable("CCM")
	Expect(ccmPath).To(BeAnExistingFile(), "CCM path %q does not exist", ccmPath)
	createRepositoryInput.RegisterClusterResourceSetConfigMapTransformation(
		ccmPath,
		"CCM_RESOURCES",
	)
	ccmSecretPath := config.MustGetVariable("CCM_SECRET")
	Expect(ccmSecretPath).To(BeAnExistingFile(), "CCM secret path %q does not exist", ccmPath)
	createRepositoryInput.RegisterClusterResourceSetConfigMapTransformation(
		ccmSecretPath,
		"CCM_SECRET_RESOURCES",
	)

	path := clusterctl.CreateRepository(ctx, createRepositoryInput)
	Expect(path).To(BeAnExistingFile(), "no clusterctl config at %s", repositoryFolder)
	return path
}
