# Kubernetes Cluster API Provider Oxide 

Kubernetes-native declarative infrastructure for [Oxide](https://oxide.computer). 

## Description

The [Cluster API](https://github.com/kubernetes-sigs/cluster-api) Provider Oxide (CAPOx) is an infrastructure provider that lets you provision and manage the lifecycle of Kubernetes clusters on the [Oxide](https://oxide.computer) cloud computer. 

The provider implements Oxide specific K8s resources:
 - OxideMachine 
 - OxideMachineTemplate
 - OxideCluster

## Getting Started

Check out the broader [Cluster API docs](https://cluster-api.sigs.k8s.io/introduction) to learn more about Cluster API.

If you're ready to dive-in and create some clusters on an Oxide rack, then follow the [CAPOx Getting Started Guide](docs/getting-started.md).

## Contributing

### Tooling

Run the following to download tools required for development: 

```
make tools
```

### Testing

To test code changes, you can use [KinD](https://kind.sigs.k8s.io/) locally as the management cluster. There are make recipes to build and deploy the controller to the local KinD cluster or a remote cluster. 

To see available recipes, check the makefile's help recipe under the "Dev Deployment" category:

```
make help
```

### Before Opening a PR

Make sure to run the following recipe to ensure all local tests and linting passes:

```
make precommit
```

The `precommit` recipe does NOT run e2e tests, so you will need to execute those separately. 
The e2e tests require that `OXIDE_HOST` and `OXIDE_TOKEN` env variables are set. 
Default values for other test configuration can be found [here](tests/e2e/config/oxide.yaml).

```
make test-e2e
```

## Release

Tag the commit you are releasing with the semver release version (i.e. v1.0.9). A github actions workflow will kick-off that will perform the release using [goreleaser](https://goreleaser.com). 

```
git tag vX.Y.Z
git push origin vX.Y.Z
```


## License

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
